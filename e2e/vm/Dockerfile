# The VM of the end-to-end tests: Ubuntu with sshd, as the GCE image has, and
# what `isotty up` expects to find or install there. There is no systemd, so
# the service commands are no-ops; what runs under them is not under test.
FROM ubuntu:24.04
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        openssh-server sudo python3 python3-tomli-w tmux auditd ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/sshd \
    && touch /var/log/audit/audit.log && chmod 600 /var/log/audit/audit.log
RUN useradd -m -s /bin/bash isotty \
    && echo 'isotty ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/isotty
COPY no-op /usr/local/sbin/systemctl
COPY no-op /usr/local/sbin/augenrules
COPY entrypoint /usr/local/sbin/entrypoint
RUN chmod 755 /usr/local/sbin/systemctl /usr/local/sbin/augenrules /usr/local/sbin/entrypoint
EXPOSE 22
ENTRYPOINT ["/usr/local/sbin/entrypoint"]
