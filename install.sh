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

./build_docker_compose.sh --load
docker compose up -d

CONTAINER_IP=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' spr-acme)
curl --fail-with-body --silent --show-error "http://127.0.0.1/firewall/custom_interface" \
  -H "Authorization: Bearer ${SPR_API_TOKEN}" \
  -X PUT \
  --data-raw "{\"SrcIP\":\"${CONTAINER_IP}\",\"Interface\":\"spr-acme\",\"Policies\":[\"wan\",\"dns\",\"api\"]}"

echo "spr-acme is installed. Open Plugins -> spr-acme to configure the DNS provider and certificates."
