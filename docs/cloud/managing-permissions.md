## Earthly project access levels

Within an Earthly project, users may be granted one of the following access levels:

- `read`: Can view the project, including the build history and build logs.
- `read+secrets`: Same as read, but can also view and use secrets.
- `write`: Everything in `read+secrets`, plus the ability to create and modify secrets.
- `admin`: Everything in `write`, plus the ability to manage the project's users.

### Managing access to an Earthly project

To grant access to an Earthly project, you must invite the user to the project. This can be done by running:

```bash
earthly project --project <project-name> member add --permission <access-level> <email>
```

{% hint style='info' %}
##### Note
You can only invite a user to a project if they are already part of the organization.
{% endhint %}

If the user is already part of the project, you can change their access level by running:

```bash
earthly project --project <project-name> member update --permission <permission> <email>
```

If you want to revoke access to an Earthly project, you can do so by running:

```bash
earthly project --project <project-name> member rm <email>
```
