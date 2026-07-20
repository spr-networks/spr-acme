#!/bin/bash
set -euo pipefail

echo "Please enter your SPR path (/home/spr/super/)"
read -r SUPERDIR
SUPERDIR="${SUPERDIR:-/home/spr/super/}"
export SUPERDIR

echo "Please enter your SPR API token:"
read -r -s SPR_API_TOKEN
printf '\n'
if [ -z "$SPR_API_TOKEN" ]; then
  echo "need api token, generate one on the auth keys page"
  exit 1
fi

mkdir -p "$SUPERDIR/configs/plugins/spr-acme" "$SUPERDIR/state/plugins/spr-acme"
printf '%s' "$SPR_API_TOKEN" > "$SUPERDIR/configs/plugins/spr-acme/api-token"
chmod 600 "$SUPERDIR/configs/plugins/spr-acme/api-token"

KRUN_MAC="02:53:50:52:4b:02"
KRUN_TAP="kacme0"
curl --fail-with-body --silent --show-error "http://127.0.0.1/device?identity=${KRUN_MAC}" \
  -H "Authorization: Bearer ${SPR_API_TOKEN}" -H "Content-Type: application/json" \
  -X PUT --data-raw "{\"MAC\":\"${KRUN_MAC}\",\"Name\":\"spr-acme\",\"Policies\":[\"wan\",\"dns\",\"api\"],\"Groups\":[]}" >/dev/null
if ! sudo nft get element inet filter dhcp_access "{ \"${KRUN_TAP}\" . ${KRUN_MAC} }" >/dev/null 2>&1; then
  sudo nft add element inet filter dhcp_access "{ \"${KRUN_TAP}\" . ${KRUN_MAC} : accept }"
fi

./build_docker_compose.sh --load
docker compose -f docker-compose-kvm.yml up -d

CONTAINER_IP=
for _ in $(seq 1 30); do
  CONTAINER_IP="$(jq -r --arg mac "$KRUN_MAC" '.[$mac].RecentIP // empty' "$SUPERDIR/state/public/devices-public.json")"
  [ -n "$CONTAINER_IP" ] && break
  sleep 1
done
[ -n "$CONTAINER_IP" ] || { echo "spr-acme did not obtain an SPR DHCP lease" >&2; exit 1; }
curl --fail-with-body --silent --show-error "http://127.0.0.1/firewall/custom_interface" \
  -H "Authorization: Bearer ${SPR_API_TOKEN}" \
  -X PUT \
  --data-raw "{\"SrcIP\":\"${CONTAINER_IP}\",\"Interface\":\"${KRUN_TAP}\",\"Policies\":[\"wan\",\"dns\",\"api\"]}"

echo "spr-acme is installed. Open Plugins -> spr-acme to configure the DNS provider and certificates."
