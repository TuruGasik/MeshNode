#!/bin/sh
# Generate CSV bootstrap auth built-in-database EMQX dari env var saat start,
# lalu jalankan EMQX (CMD stock image). Dipanggil via compose `command:`,
# jadi docker-entrypoint.sh stock tetap jalan lebih dulu dan exec script ini.
# Input pakai prefix BOOT_* (bukan EMQX_*) supaya tidak dipetakan ke config HOCON.
set -eu
CSV=/tmp/emqx-bootstrap-users.csv
umask 077
{
  echo "user_id,password,is_superuser"
  echo "${BOOT_USER_IDMESHNODE:?},${BOOT_PASS_IDMESHNODE:?},false"
  echo "${BOOT_USER_MESHMAP:?},${BOOT_PASS_MESHMAP:?},false"
  echo "${BOOT_USER_RELAY:?},${BOOT_PASS_RELAY:?},false"
  echo "${BOOT_USER_KEMPLU:?},${BOOT_PASS_KEMPLU:?},true"
  echo "${BOOT_USER_AUTONOTIF:?},${BOOT_PASS_AUTONOTIF:?},false"
} > "$CSV"
echo "gen-bootstrap-users: wrote $(wc -l < "$CSV") lines to $CSV"
exec /opt/emqx/bin/emqx foreground
