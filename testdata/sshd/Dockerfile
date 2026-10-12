# A throwaway sshd for ssh_integration_test.go. The test adds its own key at start.
FROM alpine:3.20
RUN apk add --no-cache openssh-server \
 && ssh-keygen -A \
 && adduser -D -s /bin/sh kijk && echo 'kijk:*' | chpasswd -e \
 && mkdir -m 700 /home/kijk/.ssh && chown kijk:kijk /home/kijk/.ssh
CMD ["/usr/sbin/sshd", "-D", "-e"]
