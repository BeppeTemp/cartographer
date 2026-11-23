#!/usr/bin/env bash
# nightly-lanes.sh — which lanes a nightly run asks for (.github/workflows/nightly.yml).
#
#   LANES="battery, upgrade" nightly-lanes.sh
#
# $LANES is the dispatch input: lane names split on commas, spaces around a name ignored; empty or
# `all` is every lane. Prints `<lane>=true|false` per lane and `full=true|false`: only a run of every
# lane may close the `nightly-red` issue, so a partial dispatch never clears a red night (the base of
# the next range is a green scheduled run, never a dispatch). An unknown name fails (exit 1) naming it, on stderr as a workflow error.
set -euo pipefail

all="battery quarantine upgrade"
IFS=',' read -r -a parts <<<"${LANES:-all}"
sel=" "
for p in ${parts[@]+"${parts[@]}"}; do
  p="${p#"${p%%[![:space:]]*}"}"
  p="${p%"${p##*[![:space:]]}"}"
  [ -n "$p" ] || continue
  case "$p" in
    all) sel="$sel$all " ;;
    battery | quarantine | upgrade) sel="$sel$p " ;;
    *)
      echo "::error::unknown lane '$p' in the lanes input (known: ${all// /, }, all)" >&2
      exit 1
      ;;
  esac
done
[ "$sel" != " " ] || sel=" $all "
full=true
for lane in $all; do
  case "$sel" in
    *" $lane "*) echo "$lane=true" ;;
    *) echo "$lane=false"; full=false ;;
  esac
done
echo "full=$full"
