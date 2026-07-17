#!/bin/sh
set -eu
umask 077

if [ -f /configs/base/config.sh ]; then
  set -a
  # shellcheck disable=SC1091
  . /configs/base/config.sh
  set +a
fi

mkdir -p /state/plugins/spr-acme/home /state/plugins/spr-acme/lego /state/plugins/spr-acme/certificates /configs/spr-acme
chmod 700 /state/plugins/spr-acme /state/plugins/spr-acme/home /state/plugins/spr-acme/lego /state/plugins/spr-acme/certificates /configs/spr-acme

exec /spr_acme_plugin
