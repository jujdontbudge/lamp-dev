#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Deploy tools/release/worker.js as the Cloudflare Worker "lamp-site" on lamp.eilmer.si.
#   tools/release/deploy_worker.sh WORKERS_TOKEN_FILE DNS_TOKEN_FILE
# Adds only what lamp.eilmer.si needs: the script, the route lamp.eilmer.si/*, and a
# proxied AAAA 100:: record for lamp (the usual placeholder for a Worker-only name).
# It refuses to change a record or route that already exists.
set -eu
wt=$(cat "${1:?workers token file}"); dt=$(cat "${2:?dns token file}")
cf=https://api.cloudflare.com/client/v4
here=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
j() { python3 -c "import json,sys;d=json.load(sys.stdin);assert d['success'],d['errors'];print($1)"; }
zone=$(curl -fsS -H "Authorization: Bearer $dt" "$cf/zones?name=eilmer.si" | j 'd["result"][0]["id"]')
acct=$(curl -fsS -H "Authorization: Bearer $dt" "$cf/zones/$zone" | j 'd["result"]["account"]["id"]')
n=$(curl -fsS -H "Authorization: Bearer $dt" "$cf/zones/$zone/dns_records?name=lamp.eilmer.si" | j 'len(d["result"])')
[ "$n" = 0 ] || { echo "lamp.eilmer.si already has DNS records; not touching them" >&2; exit 1; }
routes=$(curl -fsS -H "Authorization: Bearer $wt" "$cf/zones/$zone/workers/routes" | j '" ".join(r["pattern"] for r in d["result"])')
case " $routes " in *" lamp.eilmer.si/* "*) echo "route exists" >&2; exit 1 ;; esac

curl -fsS -X PUT -H "Authorization: Bearer $wt" "$cf/accounts/$acct/workers/scripts/lamp-site" \
  -F 'metadata={"main_module":"worker.js","compatibility_date":"2026-11-01"};type=application/json' \
  -F "worker.js=@$here/worker.js;type=application/javascript+module" | j '"script: "+d["result"]["id"]'
sleep 10
curl -fsS -X POST -H "Authorization: Bearer $wt" -H 'Content-Type: application/json' "$cf/zones/$zone/workers/routes" \
  -d '{"pattern":"lamp.eilmer.si/*","script":"lamp-site"}' | j '"route: "+d["result"]["id"]'
sleep 10
curl -fsS -X POST -H "Authorization: Bearer $dt" -H 'Content-Type: application/json' "$cf/zones/$zone/dns_records" \
  -d '{"type":"AAAA","name":"lamp","content":"100::","proxied":true,"comment":"Lamp: lamp-site Worker"}' | j '"dns: "+d["result"]["name"]'
