#!/bin/sh
set -eu

# This script is the WITH DOCKER wrapper. It is bind-mounted into RUN commands
# as a single file, so it cannot source buildkitd/earth-env.sh and carries its
# own copy of earth_env below.
#
# Everything the earth CLI needs to tell this script is passed as flags (see
# parse_args). Only user-facing escape hatches are read from the environment.

# earth_env SUFFIX [DEFAULT] — see buildkitd/earth-env.sh.
#
# NOTE: the EARTHLY_ fallback is a temporary shim to support the
# EARTHLY_ -> EARTH_ migration; drop it once EARTHLY_ support is officially
# removed.
earth_env() {
    eval "_earth_env_v=\${EARTH_$1-}"
    if [ -n "$_earth_env_v" ]; then
        printf '%s' "$_earth_env_v"
        return 0
    fi

    eval "_earth_env_v=\${EARTHLY_$1-}"
    if [ -n "$_earth_env_v" ]; then
        echo >&2 "WARNING: EARTHLY_$1 is deprecated. Use EARTH_$1."
        printf '%s' "$_earth_env_v"
        return 0
    fi

    printf '%s' "${2-}"
}

# Values supplied by the earth CLI via flags; see parse_args.
dockerd_data_root=""
cache_data="false"
start_compose="false"
flock_acquired="false"
compose_files=""
compose_services=""
load_files=""
parsed_count=0

# parse_args consumes the leading flags of a wrapper invocation and records how
# many arguments it consumed in parsed_count, so the caller can shift past them
# to reach the user command.
parse_args() {
    parsed_count=0

    while [ $# -gt 0 ]; do
        case "$1" in
            --)
                parsed_count=$((parsed_count+1))
                return 0
                ;;
            --data-root=*)
                dockerd_data_root="${1#*=}"
                ;;
            --cache-data)
                cache_data="true"
                ;;
            --flock-acquired)
                flock_acquired="true"
                ;;
            --start-compose)
                start_compose="true"
                ;;
            --compose-file=*)
                compose_files="$compose_files ${1#*=}"
                ;;
            --compose-service=*)
                compose_services="$compose_services ${1#*=}"
                ;;
            --load-file=*)
                load_files="$load_files ${1#*=}"
                ;;
            --image-digest=*)
                # Not used. Present only so that a changed image digest busts
                # the RUN cache.
                ;;
            -*)
                echo >&2 "dockerd-wrapper.sh: unrecognized option \"$1\"."
                echo >&2 "This usually means the earth CLI and the buildkitd image are different versions."
                exit 1
                ;;
            *)
                return 0
                ;;
        esac

        shift
        parsed_count=$((parsed_count+1))
    done
}

# apply_legacy_env_protocol reads the pre-flags EARTHLY_* protocol, which is how
# earth CLIs older than the flags refactor talk to this script. Only consulted
# when the flags did not supply the value, i.e. when the caller is such a CLI.
#
# NOTE: temporary shim to keep older CLIs working against a newer buildkitd
# image; drop it once EARTHLY_ support is officially removed.
apply_legacy_env_protocol() {
    if [ -z "${EARTHLY_DOCKERD_DATA_ROOT-}" ] && [ -z "${EARTHLY_COMPOSE_FILES-}" ]; then
        return 0
    fi

    echo >&2 "WARNING: this earth CLI passes WITH DOCKER settings via EARTHLY_* environment variables, which is deprecated. Upgrade the earth CLI to match the buildkitd image."

    dockerd_data_root="${EARTHLY_DOCKERD_DATA_ROOT-}"
    cache_data="${EARTHLY_DOCKERD_CACHE_DATA:-false}"
    start_compose="${EARTHLY_START_COMPOSE:-false}"
    compose_files="${EARTHLY_COMPOSE_FILES-}"
    compose_services="${EARTHLY_COMPOSE_SERVICES-}"
    load_files="${EARTHLY_DOCKER_LOAD_FILES-}"

    if [ -n "${EARTHLY_FLOCK_AQUIRED-}" ]; then
        flock_acquired="true"
    fi

    if [ -z "${EARTH_DOCKER_LOAD_REGISTRY-}" ] && [ -n "${EARTHLY_DOCKER_LOAD_REGISTRY-}" ]; then
        EARTH_DOCKER_LOAD_REGISTRY="$EARTHLY_DOCKER_LOAD_REGISTRY"
        export EARTH_DOCKER_LOAD_REGISTRY
    fi
}

docker_wrapper_debug="$(earth_env DOCKER_WRAPPER_DEBUG)"
if [ "$docker_wrapper_debug" = "1" ]; then
    echo "enabling docker wrapper debug mode"
    set -x
fi

# This host is used to pull images from the embedded BuildKit Docker registry.
buildkit_docker_registry='172.30.0.1:8371'

# used to prefix images that are persisted to the WITH DOCKER cache
earthly_cached_docker_image_prefix="earthly_cached_"

detect_docker_compose_cmd() {
    if command -v docker-compose >/dev/null; then
        echo "docker-compose"
        return 0
    fi
    if docker help | grep -w compose >/dev/null; then
        echo "docker compose"
        return 0
    fi
    echo >&2 "failed to detect docker compose / docker-compose command"
    return 1
}

# Runs docker-compose with the right -f flags.
docker_compose_cmd() {
    compose_file_flags=""
    for f in $compose_files; do
        compose_file_flags="$compose_file_flags -f $f"
    done
    export COMPOSE_HTTP_TIMEOUT=600
    docker_compose="$(detect_docker_compose_cmd)"
    export COMPOSE_PROJECT_NAME="default" # newer versions of docker fail if this is not set; older versions used "default" when it was not set
    # shellcheck disable=SC2086
    $docker_compose $compose_file_flags "$@"
}

write_compose_config() {
    mkdir -p /tmp/earthbuild
    docker_compose_cmd config >/tmp/earthbuild/compose-config.yml
}

execute() {
    if [ -z "$dockerd_data_root" ]; then
        echo "--data-root not set"
        exit 1
    fi
    mkdir -p "$dockerd_data_root"

    # Exported for tools in the RUN that resolve it themselves, such as
    # podman's storage.conf (see run-integration-tests.sh).
    export EARTH_DOCKERD_DATA_ROOT="$dockerd_data_root"

    if [ -f "/sys/fs/cgroup/cgroup.controllers" ] && [ "$flock_acquired" != "true" ]; then
        if [ "$docker_wrapper_debug" = "1" ]; then
            echo >&2 "detected cgroups v2"
        fi

        # move script to separate cgroup, to prevent the root cgroup from becoming threaded (which will prevent systemd images (e.g. kind) from running)
        mkdir /sys/fs/cgroup/dockerd-wrapper
        echo $$ > /sys/fs/cgroup/dockerd-wrapper/cgroup.procs

        # earthbuild wraps dockerd-wrapper.sh with a call via /bin/sh -c '....'
        # so we also need to move the parent pid into this new group, which is weird
        # TODO: we should unwrap this so $$ is all we need to move
        echo 1 > /sys/fs/cgroup/dockerd-wrapper/cgroup.procs

        if [ "$(wc -l < /sys/fs/cgroup/cgroup.procs)" != "0" ]; then
            echo >&2 "warning: processes exist in the root cgroup; this may cause errors during cgroup initialization"
        fi

        root_cgroup_type="$(cat /sys/fs/cgroup/cgroup.type)"
        if [ "$root_cgroup_type" != "domain" ]; then
            echo >&2 "WARNING: expected cgroup type of \"domain\", but got \"$root_cgroup_type\" instead"
        fi
    fi

    if [ "$cache_data" = "true" ] && [ "$flock_acquired" != "true" ]; then
        flock_path="$dockerd_data_root/.earthly-docker-lock"
        echo "acquiring flock for $flock_path"
        # dockerd-wrapper.sh will be recursively called once the lock is acquired
        flock "$flock_path" "$0" "$wrapper_cmd" --flock-acquired "$@"
        exit 0
    fi

    # Sometimes, when dockerd starts containerd, it doesn't come up in time. This timeout is not configurable from
    # dockerd, therefore we retry... since most instances of this timeout seem to be related to networking or scheduling
    # when many WITH DOCKER commands are also running. Logs are printed for each failure.
    for i in 1 2 3 4 5; do
        if start_dockerd; then
            break
        else
            if [ "$i" = 5 ]; then
                # Exiting here on the last retry maintains prior behavior of exiting when this cant start.
                exit 1
            fi

            if grep -q "^failed to start containerd: timeout waiting for containerd to start$" /var/log/docker.log; then
                # This error is the sentinel string for retrying to start dockerd.
                echo "Attempting to restart dockerd (attempt $i), since the error may be transient..."
                sleep 5
            else
                # If the logs do not contain this, then fail fast to maintain prior behavior.
                exit 1
            fi
        fi
    done

    if [ "$cache_data" = "true" ]; then
        clean_leftover_docker_objects

        # rename existing tags, so we can track which ones get re-tagged
        for img in $(docker images -q); do
            docker tag "$img" "${earthly_cached_docker_image_prefix}${img}"
        done
        docker images -a --format '{{.Repository}}:{{.Tag}}' | grep -v "^$earthly_cached_docker_image_prefix" | xargs --no-run-if-empty docker rmi --force
    fi

    load_file_images
    load_registry_images

    # delete cached images (which weren't re-tagged via the pull)
    if [ "$cache_data" = "true" ]; then
        docker images -f reference=$earthly_cached_docker_image_prefix'*' --format '{{.Repository}}:{{.Tag}}' | xargs --no-run-if-empty docker rmi --force
        docker images -f "dangling=true" -q | xargs --no-run-if-empty docker rmi --force
    fi

    if [ "$start_compose" = "true" ]; then
        # shellcheck disable=SC2086
        docker_compose_cmd up -d $compose_services
    fi

    shift "$parsed_count"
    export EARTH_WITH_DOCKER=1
    set +e
    "$@"
    exit_code="$?"
    set -e

    if [ "$start_compose" = "true" ]; then
        docker_compose_cmd down --remove-orphans
    fi
    stop_dockerd
    return "$exit_code"
}

start_dockerd() {
    if [ "$cache_data" = "true" ]; then
        data_root="$dockerd_data_root"
    else
        data_root=$(TMPDIR="$dockerd_data_root/" mktemp -d)
    fi
    echo "Starting dockerd with data root $data_root"

    if uname -a | grep microsoft-standard-WSL >/dev/null; then
        if iptables --version | grep nf_tables >/dev/null; then
            echo "WARNING: WSL and iptables-nft may not work; attempting to switch to iptables-legacy"
            ln -sf "/sbin/iptables-legacy" /sbin/iptables
        fi
    fi

    # Use a specific IP range to avoid collision with host dockerd (we need to also connect to host
    # docker containers for the debugger).
    if ! [ -f /etc/docker/daemon.json ]; then
        mkdir -p /etc/docker
        echo >/etc/docker/daemon.json '{}'
    fi

    # compliments of https://stackoverflow.com/a/53666584
    # this will concatenate arrays found in both the LHS and RHS; default jq will overwrite the LHS with the RHS
    cat <<'EOF' > /tmp/meld.jq
def meld(a; b):
  a as $a | b as $b
  | if ($a|type) == "object" and ($b|type) == "object"
    then reduce ([$a,$b]|add|keys_unsorted[]) as $k ({};
      .[$k] = meld( $a[$k]; $b[$k]) )
    elif ($a|type) == "array" and ($b|type) == "array"
    then $a+$b
    elif $b == null then $a
    else $b
    end;
meld($user; .)
EOF

    # TODO(jhorsts): enable containerd snapshotter once we have proper support for it.
    # More https://docs.docker.com/engine/storage/drivers/select-storage-driver/
    #
    # Disabling containerd snappshotter is a temporary workaround for ensuring Docker-in-Docker works in EarthBuild.
    #
    # https://github.com/EarthBuild/earthbuild/issues/195

    daemon_data="$(cat /etc/docker/daemon.json)"
    cat <<EOF | jq --argjson user "$daemon_data" -f /tmp/meld.jq > /etc/docker/daemon.json
{
    "default-address-pools" : [
        {
            "base" : "172.21.0.0/16",
            "size" : 24
        },
        {
            "base" : "172.22.0.0/16",
            "size" : 24
        }
    ],
    "bip": "172.20.0.1/16",
    "data-root": "$data_root",
    "insecure-registries" : ["$buildkit_docker_registry"],
    "registry-mirrors" : ["https://mirror.gcr.io", "https://public.ecr.aws"],
    "features": {
        "containerd-snapshotter": false
    }
}
EOF

    # Start with wiping the dir to make sure a previous interrupted build did not leave its state around.
    wipe_data_root "$data_root"
    mkdir -p "$data_root"
    rm -f /var/run/docker.pid
    dockerd >/var/log/docker.log 2>&1 &
    dockerd_pid="$!"
    i=1
    timeout=300
    while ! docker ps >/dev/null 2>&1; do
        sleep 1
        fail=false
        if [ "$i" -gt "$timeout" ]; then
            echo "ERROR: dockerd start timeout (${timeout}s)"
            fail=true
        fi
        if ! kill -0 "$dockerd_pid" >/dev/null 2>&1; then
            echo "ERROR: dockerd crashed on startup"
            fail=true
        fi
        if [ "$fail" = "true" ]; then
            # Print dockerd logs on start failure.
            print_dockerd_logs
            echo "If you are having trouble running docker, try using the official earthbuild/dind image instead"
            return 1
        fi
        i=$((i+1))
    done
}

print_dockerd_logs() {
  echo "Architecture: $(uname -m)"
  echo "==== Begin dockerd logs ===="
  cat /var/log/docker.log || true
  echo "==== End dockerd logs ===="
}

stop_dockerd() {
    dockerd_pid="$(cat /var/run/docker.pid)"
    timeout=30

    if [ -n "$dockerd_pid" ]; then
        kill "$dockerd_pid" >/dev/null 2>&1
        i=1
        while kill -0 "$dockerd_pid" >/dev/null 2>&1; do
            sleep 1
            if [ "$i" -gt "$timeout" ]; then
                echo "dockerd did not exit after $timeout seconds, force-exiting"
                kill -9 "$dockerd_pid" >/dev/null 2>&1 || true
            fi
            i=$((i+1))
        done

        # Wait for the PID to exit. This ensures that dockerd cannot keep any files in data root open.
        wait "$dockerd_pid" || true
    fi

    # Wipe dockerd data when done.
    wipe_data_root "$data_root"
}

wipe_data_root() {
    if [ "$cache_data" = "true" ]; then
        return 0
    fi
    if ! rm -rf "$1" 2>/dev/null >&2 && [ -n "$(ls -A "$1")" ]; then
        # We have some issues about failing to delete files.
        # If we fail, list the processes keeping it open for results.
        rm -rf "$1" || true # Do it again, but now print the error.
        echo "==== Begin file lsof info ===="
        if ! lsof +D "$1" ; then
            echo "Failed to run lsof +D $1. Trying lsof $1"
            if ! lsof "$1"; then
                echo "Failed to run lsof $1"
            fi
        fi
        echo "==== End file lsof info ===="
        echo "==== Begin file ls info ===="
        if ! ls -Ral "$1"; then
            echo "Failed to run ls -Ral $1"
        fi
        echo "==== End file ls info ===="
        echo "" # Add space between above and docker logs
        print_dockerd_logs
    fi
}

load_file_images() {
    if [ -n "$load_files" ]; then
        echo "Loading images from BuildKit via tar files..."
        for img in $load_files; do
            docker load -i "$img" || (stop_dockerd; exit 1)
        done
        echo "...done"
    fi
}

get_current_time_ns() {
    # Note: busybox does not support date +%s%N; instead we use stat to fetch nanosecond
    f="$(mktemp)"
    current_time="$(stat -t "$f" | awk '{print $13}')"
    current_time_ns="$(stat "$f" | grep Modify | awk '{print $3}' | awk -F . '{print $2}' | grep -o '[1-9].*')"
    rm "$f"

    # Note that the current_time_ns must not start with a 0 (which is why there is a grep [1-9]); however
    # there's an edge case where current_time_ns="00000000", which would turn into "", so we need to set it back to "0"
    if [ "$current_time_ns" = "" ]; then current_time_ns=0; fi

    test -n "$current_time" || (echo "current_time is empty" && exit 1)
    test -n "$current_time_ns" || (echo "current_time_ns is empty" && exit 1)
    current_time_combined="$((current_time*1000000000+current_time_ns))"
    echo "$current_time_combined"
}

clean_leftover_docker_objects() {
        # Kill any existing containers, and prune any resources that may have
        # been left behind from a previous execution.
        docker container ls --quiet | xargs --no-run-if-empty docker container kill
        docker container prune --force
        docker volume prune --force
        docker network prune --force
}

load_registry_images() {
    # Passed as a BuildKit secret-as-env rather than a flag, so that the
    # (per-build) intermediate image names do not bust the RUN cache.
    load_registry=${EARTH_DOCKER_LOAD_REGISTRY:-''}
    if [ -n "$load_registry" ]; then
        echo "Loading images from BuildKit via embedded registry..."

        start_time="$(get_current_time_ns)"
        bg_processes=""  # Initialize the background processes variable

        for img in $load_registry; do
            case "$img" in
                *'|'*)
                    with_reg="$buildkit_docker_registry/$(printf '%s' "$img" | cut -d'|' -f1)"
                    user_tag="$(printf '%s' "$img" | cut -d'|' -f2-)"
                    ;;
                *)
                    # Old format before v0.6.21.
                    with_reg="$buildkit_docker_registry/$img"
                    user_tag="$(printf '%s' "$img" | cut -d'/' -f2-)"
                    echo "Detected old format"
                    ;;
            esac
            echo "Pulling $with_reg and retagging as $user_tag"
            # Download and tag images in parallel
            (docker pull -q "$with_reg" && docker tag "$with_reg" "$user_tag" && docker rmi --force "$with_reg") &

            bg_processes="$bg_processes $!"

        done

        # Wait for all background processes to finish
        for pid in $bg_processes; do
            wait "$pid" || {
                echo "Downloading of images failed"
                stop_dockerd
                exit 1
            }
        done
        end_time="$(get_current_time_ns)"
        elapsed_ns="$((end_time - start_time))"
        elapsed_ms="$((elapsed_ns/1000000))"
        echo "Loading images done in ${elapsed_ms} ms"
    fi
}

docker_wrapper_debug_cmd="$(earth_env DOCKER_WRAPPER_DEBUG_CMD)"
if [ -n "$docker_wrapper_debug_cmd" ]; then
    echo "Running debug command: $docker_wrapper_debug_cmd"
    eval "$docker_wrapper_debug_cmd"
    echo "debug command exited with $?; forcing exit 1 to prevent saving RUN snapshot"
    exit 1
fi

docker_wrapper_pre_script="$(earth_env DOCKER_WRAPPER_PRE_SCRIPT "/usr/share/earthly/dockerd-wrapper-pre-script")"
if [ -f "$docker_wrapper_pre_script" ]; then
    "$docker_wrapper_pre_script"
fi

wrapper_cmd="${1-}"
shift 2>/dev/null || true

case "$wrapper_cmd" in
    get-compose-config)
        parse_args "$@"
        if [ -z "$compose_files" ]; then
            apply_legacy_env_protocol
        fi

        write_compose_config
        exit 0
        ;;

    execute)
        # Note that execute is passed the un-shifted arguments, so that it can
        # repeat the original invocation when it re-execs itself under flock.
        parse_args "$@"
        if [ -z "$dockerd_data_root" ]; then
            apply_legacy_env_protocol
        fi

        execute "$@"
        exit "$?"
        ;;

    *)
        echo "Invalid command $wrapper_cmd"
        exit 1
        ;;
esac
