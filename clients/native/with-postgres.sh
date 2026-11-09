#!/usr/bin/env bash
set -euo pipefail

# Never connect to an ambient database. Each invocation owns a socket-only cluster.
pgbin="${WUU_TEST_PG_BIN:-$(pg_config --bindir)}"
cluster="$(mktemp -d /tmp/wuu-pg.XXXXXX)"
cleanup() {
    "$pgbin/pg_ctl" -D "$cluster/data" -m immediate -w stop >/dev/null 2>&1 || true
    rm -rf "$cluster"
}
trap cleanup EXIT
"$pgbin/initdb" -D "$cluster/data" -U wuu_native -A trust --no-locale -E UTF8 >"$cluster/init.log"
"$pgbin/pg_ctl" -D "$cluster/data" -l "$cluster/server.log" -o "-h '' -k $cluster -F" -w start >/dev/null
export WUU_TEST_DATABASE_URL="postgresql://wuu_native@/postgres?host=$cluster&sslmode=disable"
"$@"
