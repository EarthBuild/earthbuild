# Public Key Authentication (Decommissioned)

{% hint style='danger' %}

##### Decommissioned with Earthly Cloud

Public-key authentication was a feature of the hosted Earthly Cloud account service. Along with other Earthly Cloud features, hosted accounts and the central authentication server were decommissioned. EarthBuild is a self-hosted build tool with no concept of accounts, organizations, or a hosted authentication server. See [Migrating from Earthly](../migrating-from-earthly.md) for details.

{% endhint %}

## Historical Overview

Earthly originally provided public-key based authentication for its hosted service. This page details how that authentication mechanism operated historically.

### What is public-key authentication

Public key authentication provides greater security compared to password authentication; it is achieved by using [asymmetric cryptography](https://en.wikipedia.org/wiki/Public-key_cryptography).

A user generates a pair of private and public keys; the public key is publicly distributed to anyone who wishes to send an encrypted message that only the holder of the private key can decrypt.
It is important that you **never share your private key**, otherwise anyone could use it to access data that is only intended for you.

Similarly, it is possible to sign data using your private key -- any user who has your public key can use it to verify the message was signed by you (or anyone who has access to your private key).

For these reasons, it is crucial that your private key remains private -- as a result, Earthly never stored or transmitted your private key.

### How did Earthly implement public-key authentication

Earthly accounts could be associated with any number of public keys (both `ssh-rsa`, and `ssh-ed25519` public keys were supported). These public keys were stored on the Earthly server, in a database
that mimics the `~/.ssh/authorized_keys` file one typically finds on a server.

The client first connected to the Earthly server over an HTTPS connection; the client responded with a [cryptographically-secure random](https://en.wikipedia.org/wiki/Cryptographically_secure_pseudorandom_number_generator) blob of data.
The client then passed that blob of data to the [ssh-agent](https://en.wikipedia.org/wiki/Ssh-agent) process running on the local host via `SSH_AUTH_SOCK`. The ssh-agent signed the blob of data, and returned the signature -- the client never read private keys directly.

This signature was sent to the Earthly server; if the signature was verified using a registered public key, then the server responded with a [JSON Web Token (JWT)](https://en.wikipedia.org/wiki/JSON_Web_Token) used for the session duration.
