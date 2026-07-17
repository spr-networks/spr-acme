#!/usr/bin/env bash
# Re-resolve build inputs, update reproducible.env, and synchronize Dockerfile
# defaults. Network lookups are read-only; review the resulting git diff.
set -euo pipefail
cd "$(dirname "$0")"

UBUNTU_TAG=ubuntu:24.04
ALPINE_TAG=alpine:latest
NODE_TAG=node:18
DOCKERFILE_TAG=docker/dockerfile:1
BUILDKIT_TAG=moby/buildkit:buildx-stable-1
CONTAINER_TEMPLATE_TAG=ghcr.io/spr-networks/container_template:latest
GO_MINOR=1.25
LEGO_REPO=https://github.com/go-acme/lego.git

mdigest() { docker buildx imagetools inspect "$1" --format '{{.Manifest.Digest}}'; }

echo "Resolving container and toolchain pins..." >&2
UBUNTU_REF="${UBUNTU_TAG}@$(mdigest "$UBUNTU_TAG")"
ALPINE_REF="${ALPINE_TAG%%:*}@$(mdigest "$ALPINE_TAG")"
NODE_REF="${NODE_TAG}@$(mdigest "$NODE_TAG")"
DOCKERFILE_SYNTAX="${DOCKERFILE_TAG}@$(mdigest "$DOCKERFILE_TAG")"
BUILDKIT_REF="${BUILDKIT_TAG}@$(mdigest "$BUILDKIT_TAG")"
CONTAINER_TEMPLATE_REF="${CONTAINER_TEMPLATE_TAG%:*}@$(mdigest "$CONTAINER_TEMPLATE_TAG")"
UBUNTU_SNAPSHOT="${UBUNTU_SNAPSHOT:-$(grep -E '^UBUNTU_SNAPSHOT=' reproducible.env | cut -d= -f2)}"
snapshot_code=$(curl -fsS -o /dev/null -w '%{http_code}' "https://snapshot.ubuntu.com/ubuntu/${UBUNTU_SNAPSHOT}/dists/noble/InRelease" || true)
[ "$snapshot_code" = "200" ] || { echo "Ubuntu snapshot ${UBUNTU_SNAPSHOT} is unavailable (HTTP ${snapshot_code})" >&2; exit 1; }

read -r GO_VERSION GO_SHA256_AMD64 GO_SHA256_ARM64 < <(
  curl -fsSL "https://go.dev/dl/?mode=json&include=all" | python3 -c '
import json,sys
minor=sys.argv[1]
versions=[v for v in json.load(sys.stdin) if v["version"].startswith("go"+minor+".")]
key=lambda v: [int(part) for part in v["version"][2:].split(".")]
version=sorted(versions,key=key)[-1]
sha={f["arch"]:f["sha256"] for f in version["files"] if f["os"]=="linux" and f["kind"]=="archive"}
print(version["version"][2:], sha["amd64"], sha["arm64"])' "$GO_MINOR"
)

echo "Resolving the latest stable lego release..." >&2
LEGO_VERSION=$(curl -fsSL https://api.github.com/repos/go-acme/lego/releases/latest \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); assert not d["prerelease"]; print(d["tag_name"])')
LEGO_COMMIT=$(git ls-remote "$LEGO_REPO" "refs/tags/${LEGO_VERSION}^{}" | cut -f1)
if [ -z "$LEGO_COMMIT" ]; then
  LEGO_COMMIT=$(git ls-remote "$LEGO_REPO" "refs/tags/${LEGO_VERSION}" | cut -f1)
fi
[ -n "$LEGO_COMMIT" ] || { echo "Could not resolve lego tag ${LEGO_VERSION}" >&2; exit 1; }

tmp_env=$(mktemp)
trap 'rm -f "$tmp_env"' EXIT
sed \
  -e "s|^UBUNTU_REF=.*|UBUNTU_REF=${UBUNTU_REF}|" \
  -e "s|^ALPINE_REF=.*|ALPINE_REF=${ALPINE_REF}|" \
  -e "s|^NODE_REF=.*|NODE_REF=${NODE_REF}|" \
  -e "s|^DOCKERFILE_SYNTAX=.*|DOCKERFILE_SYNTAX=${DOCKERFILE_SYNTAX}|" \
  -e "s|^BUILDKIT_REF=.*|BUILDKIT_REF=${BUILDKIT_REF}|" \
  -e "s|^CONTAINER_TEMPLATE_REF=.*|CONTAINER_TEMPLATE_REF=${CONTAINER_TEMPLATE_REF}|" \
  -e "s|^UBUNTU_SNAPSHOT=.*|UBUNTU_SNAPSHOT=${UBUNTU_SNAPSHOT}|" \
  -e "s|^GO_VERSION=.*|GO_VERSION=${GO_VERSION}|" \
  -e "s|^GO_SHA256_AMD64=.*|GO_SHA256_AMD64=${GO_SHA256_AMD64}|" \
  -e "s|^GO_SHA256_ARM64=.*|GO_SHA256_ARM64=${GO_SHA256_ARM64}|" \
  -e "s|^LEGO_VERSION=.*|LEGO_VERSION=${LEGO_VERSION}|" \
  -e "s|^LEGO_COMMIT=.*|LEGO_COMMIT=${LEGO_COMMIT}|" \
  reproducible.env > "$tmp_env"
chmod 0644 "$tmp_env"
mv "$tmp_env" reproducible.env
trap - EXIT

replace_line() {
  local file="$1" pattern="$2" replacement="$3" tmp
  tmp=$(mktemp)
  sed "s|${pattern}|${replacement}|" "$file" > "$tmp"
  chmod 0644 "$tmp"
  mv "$tmp" "$file"
}
while IFS='=' read -r key value; do
  case "$key" in ''|\#*) continue;; esac
  if [ "$key" = "DOCKERFILE_SYNTAX" ]; then
    replace_line Dockerfile '^# syntax=.*' "# syntax=${value}"
  else
    replace_line Dockerfile "^ARG ${key}=.*" "ARG ${key}=${value}"
  fi
done < reproducible.env

echo "Pins updated. Review with: git diff" >&2
