# Earthly Cloud

Earthly Cloud is a collection of features that enrich the Earthly experience via cloud-based services. These include:

- [Earthly Cloud Secrets](./cloud-secrets.md): A secret management system that allows you to store secrets in a cloud-based service and use them across builds.

- [OIDC Authentication](./oidc.md): The ability to authenticate to 3rd-party cloud services without storing long-term credentials.

### Creating a project
To use certain features, Earthly Cloud Secrets, you will additionally need to create an Earthly Project. You can create a project by using the CLI as described below.
```bash
earthly project create <project-name>
```
