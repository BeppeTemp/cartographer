#!/bin/sh
# Host side of the "Linux without systemd" leg (D1682): builds one static binary
# and runs test/linux/nosystemd.sh inside each image below, which have no systemd
# and no init at all. Needs Docker; the CI job `test-linux-nosystemd` and
# `make test-linux` are the callers.
#
# Usage: test/linux/run.sh
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$here/../.." && pwd)

# Pinned by digest like the Dockerfile's (both digests are multi-arch index digests, so
# an arm64 runner or a local Apple-silicon Docker resolves its own platform): a moving tag would change what this
# leg proves without a commit. alpine = musl + busybox crond; debian = glibc + cronie-like Vixie cron.
ALPINE='alpine:3.24@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35'
DEBIAN='debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc'

arch=$(docker version --format '{{.Server.Arch}}')
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$out/cartographer" ./cmd/cartographer)

# Runs as root inside the container: tools, two accounts, a cron daemon, a PATH
# without `crontab` for the second scenario, then each scenario as its own
# non-root account with an empty environment (HOME is the account's home, so the
# scheduler guard allows crontab).
bootstrap='
set -eu
if command -v apk >/dev/null 2>&1; then
  apk add --no-cache git curl busybox-suid >/dev/null
  adduser -D user; adduser -D user2
  crond -b -l 8
else
  apt-get update -qq >/dev/null
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq git curl cron >/dev/null
  useradd -m user; useradd -m user2
  cron
fi
install -m 755 /cartographer /usr/local/bin/cartographer
mkdir /opt/nocron
for d in /usr/local/bin /usr/bin /bin /usr/sbin /sbin; do
  for f in "$d"/*; do
    n=${f##*/}
    [ "$n" = crontab ] || [ -e "/opt/nocron/$n" ] || ln -s "$f" "/opt/nocron/$n"
  done
done
runas() { # account path mode
  su "$1" -s /bin/sh -c "cd; exec /usr/bin/env -i HOME=/home/$1 USER=$1 PATH=$2 /bin/sh /nosystemd.sh $3"
}
echo "== $(uname -m), $(. /etc/os-release; echo "$PRETTY_NAME")"
runas user /usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin cron
runas user2 /opt/nocron nocron
'

for image in "$ALPINE" "$DEBIAN"; do
  echo "=== $image"
  docker run --rm \
    -v "$out/cartographer:/cartographer:ro" \
    -v "$here/nosystemd.sh:/nosystemd.sh:ro" \
    "$image" /bin/sh -c "$bootstrap"
done
echo "test/linux: all images passed"
