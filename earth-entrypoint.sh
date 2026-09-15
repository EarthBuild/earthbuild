#!/bin/sh
set -e

# shellcheck source-path=SCRIPTDIR
# shellcheck source=earth-env.sh
. /usr/bin/earth-env.sh

EARTH_DEBUG="$(earth_env DEBUG false)"
if [ "$EARTH_DEBUG" = "true" ]; then
    set -x
    export EARTH_DEBUG
fi

# The CLI writes its generated certificates to ~/.<installation name>/certs.
# +earthly-docker bakes the name it built the CLI with into the environment.
# A caller overriding the installation name moves that directory, so resolve it
# the same way the CLI does, falling back to the name the image was built with.
earth_installation_name="$(earth_env INSTALLATION_NAME "${EARTH_IMAGE_INSTALLATION_NAME:-earth}")"
earth_certs_dir="${HOME:-/root}/.${earth_installation_name}/certs"

# A mount point for callers supplying their own config, passed explicitly via
# --config below. It tracks the installation name so it stays alongside the
# other per-installation paths.
earth_config="/etc/.${earth_installation_name}/config.yml"

# The pre-rename location, honoured for one deprecation cycle so a caller still
# mounting a config there keeps working. Only consulted when nothing is mounted
# at the current location, so a caller supplying both is not surprised.
earth_config_deprecated="/etc/.earthly/config.yml"
if [ ! -f "$earth_config" ] && [ -f "$earth_config_deprecated" ]; then
  earth_config="$earth_config_deprecated"
fi

if [ ! -f "$earth_config" ]; then
  # Missing config, generate it and use the env vars
  # Do not do both, since that would write to the mounted config
  mkdir -p "$(dirname "$earth_config")" && touch "$earth_config"

  # Apply global configuration
  if [ -n "$GLOBAL_CONFIG" ]; then
    earth --config "$earth_config" config global "$GLOBAL_CONFIG"
  fi

  # Apply git configuration
  if [ -n "$GIT_CONFIG" ]; then
    earth --config "$earth_config" config git "$GIT_CONFIG"
  fi
fi

# If no host specified, start an internal buildkit. If it is specified, rely on external setup
if [ -z "$NO_BUILDKIT" ]; then
  if [ -z "$BUILDKIT_HOST" ]; then
    if ! captest --text | grep sys_admin > /dev/null; then
      echo 1>&2 "Container appears to be running unprivileged. Currently, privileged mode is required when buildkit runs inside the container."
      echo 1>&2 "To run this image without buildkit, set the environment variable NO_BUILDKIT=1"
      exit 1
    fi

    if [ -f "/sys/fs/cgroup/cgroup.controllers" ]; then
        echo >&2 "detected cgroups v2; earth-entrypoint.sh running under pid=$$ with controllers \"$(cat /sys/fs/cgroup/cgroup.controllers)\" in group $(cat /proc/self/cgroup)"
        test "$(cat /sys/fs/cgroup/cgroup.type)" = "domain" || (echo >&2 "WARNING: invalid root cgroup type: $(cat /sys/fs/cgroup/cgroup.type)")
    fi

    # generate certificates
    earth --config "$earth_config" --buildkit-host=tcp://127.0.0.1:8372 bootstrap --certs-hostname="$(hostname)" --no-buildkit --force-certificate-generation

    # Fail loudly rather than leaving dangling symlinks: if the CLI's
    # installation name and $earth_certs_dir ever disagree, buildkitd would
    # otherwise start and fail its TLS handshake with no hint why.
    if [ ! -d "$earth_certs_dir" ]; then
      echo >&2 "earth-entrypoint.sh: expected generated certificates in $earth_certs_dir, but that directory does not exist"
      exit 1
    fi

    if [ ! -d /etc/earth-certs ] && [ ! -L /etc/earth-certs ]; then
      ln -s "$earth_certs_dir" /etc/earth-certs
    fi


    export BUILDKIT_TCP_TRANSPORT_ENABLED=true
    export BUILDKIT_TLS_ENABLED=true

    /usr/bin/entrypoint.sh \
      buildkitd \
        --config=/etc/buildkitd.toml \
        >/var/log/buildkitd.log 2>&1 \
        &

    if [ "$BUILDKIT_DEBUG" = "true" ]; then
        tail -f /var/log/buildkitd.log &
    fi

    EARTH_BUILDKIT_HOST="tcp://$(hostname):8372" # hostname is not recognized as local for this reason
    export EARTH_BUILDKIT_HOST
  else
    export EARTH_BUILDKIT_HOST="$BUILDKIT_HOST"
  fi
  ! "$EARTH_DEBUG" || echo 1>&2 "Using $EARTH_BUILDKIT_HOST as buildkit daemon"
fi

if [ -n "$SRC_DIR" ]; then
  echo 1>&2 'Please note that SRC_DIR is deprecated. This script will no longer automatically switch to it in the future.'
  echo 1>&2 'Please change the container'"'"'s working directory instead (e.g. via docker run -w)'
  cd "$SRC_DIR"
fi

EARTH_EXEC_CMD="$(earth_env EXEC_CMD)"
if [ -n "$EARTH_EXEC_CMD" ]; then
    export earth_config
    # Deprecated alias, kept for one cycle for callers reading $earthly_config.
    export earthly_config="$earth_config"
    exec "$EARTH_EXEC_CMD"
    exit 1 # this should never be reached
fi

# Run earth with given args.
# Exec so we don't have to trap and manage signal propagation
exec earth --config "$earth_config" "$@"
