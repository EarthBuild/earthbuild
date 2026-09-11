# Managing cache

This page describes how to manage the EarthBuild cache locally or on a remote runner.

## Local cache

### Local cache location

EarthBuild cache is persisted in a Docker, Podman, or Apple Container volume called `earth-cache` on your system. When earth starts for the first time, it brings up a BuildKit daemon in a container, which initializes the `earth-cache` volume. The volume is managed by EarthBuild's BuildKit daemon and there is a regular garbage-collection for old cache.

### Specifying the local cache size limit

The default cache size is adaptable depending on available space on your system. It defaults to `min(55%, max(10%, 20GB))`. If you would like to change the cache size, you can specify a different limit by modifying the `cache_size_mb` and/or `cache_size_pct` settings in the [configuration](../earth-config/earth-config.md). For example:

```yaml
global:
  cache_size_mb: 30000
  cache_size_pct: 70
```

{% hint style='info' %}

#### Checking current size of the cache volume

You can check the current size of the cache volume by running:

```bash
sudo du -h /var/lib/docker/volumes/earth-cache | tail -n 1
```

{% endhint %}

### Resetting the local cache

To reset the cache, you can issue the command

```bash
earth prune
```

You can also safely delete the cache manually, if the daemon is not running

```bash
docker stop earth-buildkitd
docker rm earth-buildkitd
docker volume rm earth-cache
```

EarthBuild also has a command that automates the above:

```bash
earth prune --reset
```

## Cache on a remote runner

### Configuring the cache size on a remote runner

Remote runners are self-hosted. You can configure the cache policy by passing the appropriate [buildkit configuration](https://github.com/moby/buildkit/blob/master/docs/buildkitd.toml.md) to the [BuildKit container](../ci-integration/remote-buildkit.md).

### Resetting the cache on a remote runner

The command `earth prune` will work on remote runners too, albeit without the `--reset` flag, which is not supported in a remote setting.

## Auto-skip cache

The auto-skip cache is used to skip parts of a build when inputs have not changed, via the `earth --auto-skip` and `BUILD --auto-skip` flags.

The skip-set is stored in a local database specified via `--auto-skip-db-path`. To clear or reset the auto-skip cache, delete the local database file directly. Note that the legacy cloud backend and `earth prune-auto-skip` command have been removed.
