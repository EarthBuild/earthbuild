FROM alpine:3.24.1

RUN apk add --no-cache openssh git \
    && ssh-keygen -A \
    && echo 'root:root' | chpasswd \
    && printf '%s\n' \
        'Port 22' \
        'PermitRootLogin yes' \
        'PasswordAuthentication yes' \
        'PubkeyAuthentication yes' \
        'AuthorizedKeysFile .ssh/authorized_keys' \
        'PubkeyAcceptedAlgorithms +ssh-rsa' \
        'HostKeyAlgorithms +ssh-rsa' \
        'StrictModes no' \
        'Subsystem sftp internal-sftp' \
        > /etc/ssh/sshd_config \
    && mkdir -p -m 700 /root/.ssh \
    && mkdir -p -m 755 /run/sshd /var/empty

EXPOSE 22

CMD ["/usr/sbin/sshd", "-D", "-e"]

