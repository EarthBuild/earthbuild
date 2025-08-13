# Managing cache

This page describes how to manage the EarthBuild cache locally or with remote BuildKit.

## Local cache

### Local cache location

earthbuild cache is persisted in a docker (or podman) volume called `earthbuild-cache` on your system. When earthbuild starts for the first time, it brings up a BuildKit daemon in a Docker container, which initializes the `earthbuild-cache` volume. The volume is managed by earthbuild's BuildKit daemon and there is a regular garbage-collection for old cache.

### Specifying the local cache size limit

The default cache size is adaptable depending on available space on your system. It defaults to `min(55%, max(10%, 20GB))`. If you would like to change the cache size, you can specify a different limit by modifying the `cache_size_mb` and/or `cache_size_pct` settings in the [configuration](../earthbuild-config/earthbuild-config.md). For example:

```yaml
global:
  cache_size_mb: 30000
  cache_size_pct: 70
```

{% hint style='info' %}
#### Checking current size of the cache volume
You can check the current size of the cache volume by running:

```bash
sudo du -h /var/lib/docker/volumes/earthbuild-cache | tail -n 1
```
{% endhint %}

### Resetting the local cache

To reset the cache, you can issue the command

```bash
earthbuild prune
```

You can also safely delete the cache manually, if the daemon is not running

```bash
docker stop earthbuild-buildkitd
docker rm earthbuild-buildkitd
docker volume rm earthbuild-cache
```

earthbuild also has a command that automates the above:

```bash
earthbuild prune --reset
```

## Cache with remote BuildKit

### Configuring the cache size on a remote runner

If you are using remote BuildKit, you can configure it with more disk space to handle larger cache sizes.

You can configure the cache policy by passing the appropriate [buildkit configuration](https://github.com/moby/buildkit/blob/master/docs/buildkitd.toml.md) to the [BuildKit container](../ci-integration/remote-buildkit.md).

### Reset cache on remote BuildKit

Remote BuildKit instances, just like local environments, can accumulate cache over time. Sometimes, that cache can become corrupted or otherwise unusable. If you would like to clear the cache on remote BuildKit, you can do so by restarting the BuildKit instance with a fresh cache.

To restart remote BuildKit with a fresh cache, you'll need to restart the BuildKit container or service according to your deployment method.

## Auto-skip cache

The auto-skip cache is a cache that is used to skip large parts of a build in certain situations. It is used by the `earthbuild --auto-skip` and `BUILD --auto-skip` commands.

Unlike the layer cache and the cache mounts, the auto-skip cache is global and is stored in a cloud database.

To clear the entire auto-skip cache for your earthbuild org, you can use the command `earthbuild prune-auto-skip`.

To clear the auto-skip cache for an entire repository, you can use the command `earthbuild prune-auto-skip --path github.com/foo/bar --deep`.

To clear the auto-skip cache for a specific target, you can use the command `earthbuild prune-auto-skip --path github.com/foo/bar --target +my-target`.
