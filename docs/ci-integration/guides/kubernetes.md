# Kubernetes

{% hint style='info' %}

##### Note

This guide describes deploying BuildKit to Kubernetes by hand.
[`EarthBuild/helm-buildkitd`](https://github.com/EarthBuild/helm-buildkitd) is a Helm chart for the
same job, with scale-to-zero support, and is intended to supersede this guide. It is currently
**alpha** -- no tagged release yet -- so the manual setup below remains the supported path for now.

{% endhint %}

## Overview

Kubernetes isn't a CI per-se, but it _can_ serve as the underpinning for many modern CI systems. As such, this example serves as a bare-bones example to base your implementations on.

### Compatibility

`earth` has been tested with the all-in-one `earthbuild/earthbuild` mode, and works as long as the pod runs in a `privileged` mode.

It has also been tested with a _single_ remote `earthbuild/buildkitd` running in `privileged` mode, and an `earthbuild/earthbuild` pod running without any additional security concerns. This configuration is considered experimental. See [these additional instructions](../remote-buildkit.md).

Multi-node `earthbuild/buildkitd` configurations are currently unsupported.

### Resources

- [Kubernetes Documentation](https://kubernetes.io/docs/home/supported-doc-versions/)
- [Kubernetes Taints & Tolerations](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/)

## Setup (`earthbuild/earthbuild` Only)

This is the recommended approach when using EarthBuild within Kubernetes. Assuming you are following the steps outlined in the [overview](../overview.md), here are the additional things you need to configure:

### Dependencies

Your Kubernetes cluster needs to allow `privileged` mode pods. It's possible to use a separate instance group, along with Taints and Tolerations to effectively segregate these pods.

### Installation

The default image from `earthbuild/earthbuild` should be sufficient. If you need additional tools or configuration, you can [create your own runner image](../build-an-earthly-ci-image.md).

### Configuration

In some instances, notably when using [Calico](https://www.tigera.io/project-calico/) within your cluster, the MTU of the clusters network may end up mismatched with the internal CNI network, preventing external communication. You can set this through the `CNI_MTU` environment variable to force a match.

`earthbuild/earthbuild` currently requires the use of privileged mode. Use this in your container spec to enable it:

```yaml
securityContext:
  privileged: true
```

The `earthbuild/earthbuild` container will operate best when provided with decent storage for intermediate operations. Mount a volume like this:

```yaml
volumeMounts:
  - mountPath: /tmp/earthbuild
    name: buildkitd-temp
...
volumes:
  - name: buildkitd-temp
    emptyDir: {} # Or other volume type
```

The location within the container for this temporary folder is configurable with the `EARTH_TMP_DIR` environment variable.

The `earthbuild/earthbuild` image will expect to find the source code (with `Earthfile`) rooted in the default working directory, which is set to `/workspace`.

## Setup (Remote `earthbuild/buildkitd`)

{% hint style='danger' %}

##### Note

This an _experimental_ configuration.

{% endhint %}

It is possible to run multiple `earthbuild/buildkitd` instances in Kubernetes, for larger deployments. Follow the configuration instructions for using the `earthbuild/earthbuild` image above.

There are some caveats that come with this kind of a setup, though:

1. Some local cache is not available across instances, so it may take a while for the cache to become "warm".
2. Builds that occur across multiple instances simultaneously may fail in odd ways. This is not supported.
3. The TLS configuration needs to be shared across the entire fleet.

### Configuration

To mitigate some of the issues, it is recommended to run in a "sticky" mode to keep builds pinned to a single instance for the duration. You can see how to do this in our example:

```yaml
# Use session affinity to prevent "roaming" across multiple BuildKit instances; if needed.
sessionAffinity: ClientIP
sessionAffinityConfig:
  clientIP:
    timeoutSeconds: 600 # This should be longer than your build.
```

## Example

{% hint style='danger' %}

##### Note

This example is not production ready, and is intended to showcase configuration needed to get EarthBuild off the ground. If you run into any issues, or need help, [don't hesitate to reach out](https://github.com/earthbuild/earthbuild/issues/new)!

{% endhint %}

This example requires `kind` and `kubectl`:

- [`kind` Quick Start](https://kind.sigs.k8s.io/docs/user/quick-start/)
- [Install `kubectl`](https://kubernetes.io/docs/tasks/tools/#kubectl)

Create a cluster:

```bash
kind create cluster --name earthdemo
```

### All-in-one

The simplest setup runs the all-in-one image as a `Job`, with its embedded `buildkitd`. It needs
`privileged`, since `buildkitd` does not currently support rootless mode:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: earth
  labels:
    app: earth
spec:
  template:
    metadata:
      name: earth
      labels:
        app: earth
        component: earth
    spec:
      restartPolicy: Never
      containers:
      - name: earth
        image: earthbuild/earthbuild:v0.8.18
        args: ["github.com/EarthBuild/hello-world:main+hello"]
        securityContext:
          privileged: true
        volumeMounts:
          - mountPath: /workspace
            name: workspace
        env:
          # To build and save Docker images, provide the DOCKER_HOST variable instead.
          - name: NO_DOCKER
            value: '1'
      volumes:
        - name: workspace
          emptyDir: {}
  backoffLimit: 4
```

Apply it, then follow the build:

```bash
kubectl apply -f earth-job.yaml
kubectl logs -f job/earth
```

### Remote `buildkitd`

To keep a warm cache across builds, run `buildkitd` as its own `Deployment` and point the client at
it. Only the daemon needs to be privileged:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: earth-buildkitd
  labels:
    app: earth
    component: buildkitd
spec:
  replicas: 1
  selector:
    matchLabels:
      app: earth
      component: buildkitd
  template:
    metadata:
      labels:
        app: earth
        component: buildkitd
    spec:
      containers:
      - name: buildkitd
        image: earthbuild/buildkitd:v0.8.17
        # EarthBuild's buildkit daemon does not currently support rootless modes.
        securityContext:
          privileged: true
        volumeMounts:
          - mountPath: /tmp/earthbuild
            name: buildkitd-temp
        env:
          # This needs to be on to allow remote EarthBuild clients.
          - name: BUILDKIT_TCP_TRANSPORT_ENABLED
            value: 'true'
          # This should be enabled, and certificates configured, in a production environment.
          - name: BUILDKIT_TLS_ENABLED
            value: 'false'
      volumes:
        - name: buildkitd-temp
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: earth-buildkitd
  labels:
    app: earth
    component: buildkitd
spec:
  selector:
    app: earth
    component: buildkitd
  # Use session affinity to prevent "roaming" across multiple buildkit instances; if needed.
  sessionAffinity: ClientIP
  sessionAffinityConfig:
    clientIP:
      timeoutSeconds: 600 # This should be longer than your build.
  ports:
    - protocol: TCP
      port: 8372
      targetPort: 8372
```

The client `Job` then points at the service and needs no privileges of its own:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: earth
  labels:
    app: earth
    component: earth
spec:
  template:
    metadata:
      name: earth
      labels:
        app: earth
        component: earth
    spec:
      restartPolicy: Never
      containers:
      - name: earth
        image: earthbuild/earthbuild:v0.8.18
        args: ["github.com/EarthBuild/hello-world:main+hello"]
        volumeMounts:
          - mountPath: /workspace
            name: workspace
        env:
          # To build and save Docker images, provide the DOCKER_HOST variable instead.
          - name: NO_DOCKER
            value: '1'
          - name: BUILDKIT_HOST
            value: tcp://earth-buildkitd.default.svc.cluster.local:8372
          # This should be enabled, and certificates configured, in a production environment.
          - name: BUILDKIT_TCP_TRANSPORT_ENABLED
            value: 'false'
      volumes:
        - name: workspace
          emptyDir: {}
  backoffLimit: 4
```

To tear the cluster down:

```bash
kind delete cluster --name earthdemo
```
