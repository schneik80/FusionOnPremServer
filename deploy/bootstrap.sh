#!/usr/bin/env bash
# One-time provisioning for a fusionlocalserver production host (Ubuntu 24.04,
# run as root). Idempotent: safe to re-run after partial failures or to pick up
# changes to the unit/sudoers files.
#
# Usage:
#   sudo ./bootstrap.sh "ssh-ed25519 AAAA... deploy@github-actions"
#
# The argument is the deploy SSH *public* key GitHub Actions will authenticate
# with (generate the pair with: ssh-keygen -t ed25519 -f deploy_key -N "" -C
# deploy@github-actions; the private half becomes the DEPLOY_SSH_KEY repo
# secret). Omit it to skip authorized_keys setup on a re-run.
#
# What this script does NOT do (deployment-specific; printed as next steps):
# fill /etc/fusionlocalserver/env, set the Caddy hostname, create the DNS
# record, install the first binary, or register the APS callback. See
# docs/deploy/production-server.md for the full runbook.

set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
	echo "bootstrap.sh must run as root" >&2
	exit 1
fi

here="$(cd "$(dirname "$0")" && pwd)"
deploy_pubkey="${1:-}"

echo "== users =="
if ! id fls >/dev/null 2>&1; then
	useradd --system --create-home --home-dir /var/lib/fusionlocalserver \
		--shell /usr/sbin/nologin fls
	echo "created system user fls"
fi
if ! id deploy >/dev/null 2>&1; then
	useradd --create-home --shell /bin/bash deploy
	echo "created user deploy"
fi

echo "== directories =="
# deploy owns the binary dir so the release workflow can do the atomic swap
# without sudo; its one sudo privilege is restarting the service.
install -d -o deploy -g deploy -m 0755 /opt/fusionlocalserver/bin
install -d -o root -g root -m 0700 /etc/fusionlocalserver

echo "== systemd unit =="
install -o root -g root -m 0644 "$here/fusionlocalserver.service" \
	/etc/systemd/system/fusionlocalserver.service
if [ ! -f /etc/fusionlocalserver/env ]; then
	install -o root -g root -m 0600 "$here/env.example" /etc/fusionlocalserver/env
	echo "installed /etc/fusionlocalserver/env from template — EDIT IT before starting"
fi
systemctl daemon-reload
systemctl enable fusionlocalserver.service >/dev/null 2>&1 || systemctl enable fusionlocalserver.service

echo "== sudoers =="
visudo -cf "$here/sudoers-deploy"
install -o root -g root -m 0440 "$here/sudoers-deploy" \
	/etc/sudoers.d/deploy-fusionlocalserver

if [ -n "$deploy_pubkey" ]; then
	echo "== deploy SSH key =="
	install -d -o deploy -g deploy -m 0700 /home/deploy/.ssh
	touch /home/deploy/.ssh/authorized_keys
	chown deploy:deploy /home/deploy/.ssh/authorized_keys
	chmod 0600 /home/deploy/.ssh/authorized_keys
	if ! grep -qxF "$deploy_pubkey" /home/deploy/.ssh/authorized_keys; then
		printf '%s\n' "$deploy_pubkey" >>/home/deploy/.ssh/authorized_keys
		echo "added deploy public key"
	fi
fi

echo "== sshd hardening =="
install -d -m 0755 /etc/ssh/sshd_config.d
cat >/etc/ssh/sshd_config.d/90-fusionlocalserver.conf <<'CONF'
PasswordAuthentication no
PermitRootLogin prohibit-password
CONF
systemctl reload ssh || systemctl reload sshd

echo "== firewall (ufw) =="
# The app binary binds 0.0.0.0:8080 unconditionally — this firewall is what
# keeps the plain-HTTP port off the internet. Only ssh + Caddy are reachable.
ufw allow OpenSSH >/dev/null
ufw allow 80/tcp >/dev/null
ufw allow 443/tcp >/dev/null
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
ufw --force enable >/dev/null
ufw status verbose

echo "== caddy =="
if ! command -v caddy >/dev/null 2>&1; then
	apt-get update -qq
	apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https curl
	curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' |
		gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
	curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' |
		tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
	apt-get update -qq
	apt-get install -y -qq caddy
fi
install -o root -g root -m 0644 "$here/Caddyfile.example" /etc/caddy/Caddyfile.fls-example

cat <<'NEXT'

== bootstrap complete — remaining manual steps ==

1. Edit /etc/fusionlocalserver/env: APS credentials, region, PUBLIC_URL, and a
   NON-EMPTY FLS_ADMIN_USERS (required before this host is publicly reachable).
2. Merge /etc/caddy/Caddyfile.fls-example into /etc/caddy/Caddyfile with your
   real hostname, then: systemctl reload caddy
3. Create the DNS A record for the hostname -> this server's IP.
4. First binary install (later releases deploy automatically):
     curl -LO https://github.com/schneik80/fusionlocalserver/releases/latest/download/fusionlocalserver-<version>-linux-amd64.tar.gz
     tar xzf fusionlocalserver-*-linux-amd64.tar.gz fusionlocalserver
     install -o deploy -g deploy -m 0755 fusionlocalserver /opt/fusionlocalserver/bin/fusionlocalserver
     systemctl start fusionlocalserver
5. Register <PUBLIC_URL>/api/auth/callback on the APS app (exact match).
6. GitHub repo config for automated deploys:
     secret   DEPLOY_SSH_KEY     = the deploy private key
     variable DEPLOY_HOST        = the hostname
     variable DEPLOY_KNOWN_HOSTS = output of: ssh-keyscan <hostname>
See docs/deploy/production-server.md for verification and rollback.
NEXT
