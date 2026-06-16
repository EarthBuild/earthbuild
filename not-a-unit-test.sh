#!/bin/sh
set -e # dont use -x, as it will leak credentials

# This is not a unit test, as it requires access to docker hub, as well as docker/podman

if [ "$USE_EARTHLY_MIRROR" = "true" ]; then
  if [ -n "$DOCKERHUB_MIRROR" ]; then
    echo >&2 "error: DOCKERHUB_MIRROR should be empty when using the USE_EARTHLY_MIRROR option"
    exit 1
  fi
  DOCKERHUB_MIRROR="mirror.gcr.io"
  DOCKERHUB_MIRROR_AUTH="false"
fi

# first setup podman
cat > /etc/containers/containers.conf <<EOF
[containers]
netns="host"
userns="host"
ipcns="host"
utsns="host"
cgroupns="host"
cgroups="disabled"
log_driver = "k8s-file"
[engine]
cgroup_manager = "cgroupfs"
events_logger="file"
runtime="crun"
EOF

chmod 644 /etc/containers/containers.conf
sed -i -e 's|^#mount_program|mount_program|g' -e '/additionalimage.*/a "/var/lib/shared",' -e 's|^mountopt[[:space:]]*=.*$|mountopt = "nodev,fsync=0"|g' /etc/containers/storage.conf

mkdir -p /var/lib/shared/overlay-images
touch /var/lib/shared/overlay-images/images.lock

mkdir -p /var/lib/shared/overlay-layers
touch /var/lib/shared/overlay-layers/layers.lock

mkdir -p /var/lib/shared/vfs-images
touch /var/lib/shared/vfs-images/images.lock

mkdir -p /var/lib/shared/vfs-layers
touch /var/lib/shared/vfs-layers/layers.lock

# The single-quoted sed replacement is an intentional literal env-var token,
# not a value to expand at this point.
# shellcheck disable=SC2016
sed -i 's/\/var\/lib\/containers\/storage/$EARTHLY_DOCKERD_DATA_ROOT/g' /etc/containers/storage.conf

if [ -n "$DOCKERHUB_MIRROR" ]; then
    INSECURE=$(if [ "$DOCKERHUB_MIRROR_HTTP" = "true" ] || [ "$DOCKERHUB_MIRROR_INSECURE" = "true" ]; then echo 'true'; else echo 'false'; fi)
    echo "
[[registry]]
prefix=\"docker.io\"
insecure=$INSECURE
location=\"$DOCKERHUB_MIRROR\"
" > /etc/containers/registries.conf
fi

# then do a docker login (if applicable)
if [ "$DOCKERHUB_MIRROR_AUTH" = "true" ]
then
  (test -n "$DOCKERHUB_MIRROR_USER" || (echo "ERROR: DOCKERHUB_MIRROR_USER not set"; exit 1))
  (test -n "$DOCKERHUB_MIRROR_PASS" || (echo "ERROR: DOCKERHUB_MIRROR_PASS not set"; exit 1))
  if [ -n "$DOCKERHUB_MIRROR" ]
  then
    docker login "$DOCKERHUB_MIRROR" --username="$DOCKERHUB_MIRROR_USER" --password="$DOCKERHUB_MIRROR_PASS"
    podman login "$DOCKERHUB_MIRROR" --username="$DOCKERHUB_MIRROR_USER" --password="$DOCKERHUB_MIRROR_PASS"
  else
    docker login --username="$DOCKERHUB_MIRROR_USER" --password="$DOCKERHUB_MIRROR_PASS"
    podman login --username="$DOCKERHUB_MIRROR_USER" --password="$DOCKERHUB_MIRROR_PASS"
  fi
fi

# then run the test
# pkgname/testname come from the Earthfile env (ARG pkgname / ARG testname),
# which the linter can't see. Build the arg list with set -- so -run "$testname"
# is quoted; pkgname is left unquoted on purpose as it may expand to several
# space-separated package patterns.
set -- -timeout 20m -json
[ -n "$testname" ] && set -- "$@" -run "$testname"
# shellcheck disable=SC2086,SC2154
go test "$@" $pkgname | ./testparser
