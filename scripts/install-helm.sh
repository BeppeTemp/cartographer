#!/usr/bin/env bash
# Installs the pinned Helm release (linux/amd64) into the directory given as $1, after
# verifying the archive's sha256. One script for ci.yml, release.yml and preview.yml, so the
# pin lives once (D1441). A bump is a reviewed edit of the two lines below, like syft's.
set -euo pipefail

HELM_VERSION=v3.22.1
HELM_SHA256=896cf63684a42a723d61c980369d6e588330927deb13676ca0d85006dd6da0ea

dest=${1:?usage: install-helm.sh <directory>}
archive=helm-${HELM_VERSION}-linux-amd64.tar.gz
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "$tmp/$archive" "https://get.helm.sh/$archive"
echo "${HELM_SHA256}  $tmp/$archive" | sha256sum -c -
tar -xzf "$tmp/$archive" -C "$tmp" linux-amd64/helm
mkdir -p "$dest"
install -m 0755 "$tmp/linux-amd64/helm" "$dest/helm"
"$dest/helm" version --short
