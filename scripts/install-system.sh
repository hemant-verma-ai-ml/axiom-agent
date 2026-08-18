#!/usr/bin/env bash
# AXIOM Agent — system-install provisioning script.
#
# Creates the dedicated service account, state directory, and the
# random key file used by the encrypted-file credential fallback
# (Tier 2 Task 7.2 #8). Must be run as root. Idempotent — safe to
# re-run; will not overwrite an existing key.
#
# Deliberately does NOT derive the key from /etc/machine-id: that
# file is world-readable by default and systemd itself documents it
# as non-confidential, so it would provide no real protection as
# sole key material. Instead this generates a fresh random key —
# same trust model as an SSH host key.

set -euo pipefail

SERVICE_USER="axiom-agent"
STATE_DIR="/var/lib/axiom-agent"
KEY_PATH="${STATE_DIR}/key.bin"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "error: must be run as root" >&2
  exit 1
fi

if ! id "${SERVICE_USER}" &>/dev/null; then
  echo "creating system user ${SERVICE_USER}..."
  useradd --system --no-create-home --shell /usr/sbin/nologin "${SERVICE_USER}"
else
  echo "system user ${SERVICE_USER} already exists, skipping creation"
fi

echo "creating ${STATE_DIR}..."
mkdir -p "${STATE_DIR}"
chown "${SERVICE_USER}:${SERVICE_USER}" "${STATE_DIR}"
chmod 0700 "${STATE_DIR}"

if [[ -f "${KEY_PATH}" ]]; then
  echo "key file ${KEY_PATH} already exists — leaving it in place."
  echo "(re-running this script never rotates the key; rotate explicitly and"
  echo " separately if that's ever needed, since it invalidates any existing"
  echo " encrypted credentials.)"
else
  echo "generating new key at ${KEY_PATH}..."
  # 32 bytes for AES-256, straight from the kernel CSPRNG.
  head -c 32 /dev/urandom > "${KEY_PATH}"
  chown "${SERVICE_USER}:${SERVICE_USER}" "${KEY_PATH}"
  chmod 0600 "${KEY_PATH}"
fi

echo "done. verify with: ls -la ${STATE_DIR}"
