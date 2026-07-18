#!/usr/bin/env bash
# lib/kb.sh — operator functions for preparing KBs in the E2E tests.

# kb_make <abs_dir>
#   Creates the given directory (mkdir -p).
kb_make() {
    local dir="$1"
    if [[ -z "$dir" ]]; then
        echo "[ERROR] kb_make: missing argument (dir)" >&2
        return 1
    fi
    mkdir -p "$dir"
    echo "[kb] directory created: ${dir}" >&2
}

# kb_copy_fixture <fixture_name> <dest_dir>
#   Copies the given fixture (from test/e2e/fixtures/<fixture_name>/) into <dest_dir>.
#   dest_dir is created if it does not exist.
#   Does NOT include .git (fixtures have no .git; the server creates the repo with --init).
kb_copy_fixture() {
    local fixture_name="$1"
    local dest_dir="$2"

    if [[ -z "$fixture_name" || -z "$dest_dir" ]]; then
        echo "[ERROR] kb_copy_fixture: missing arguments (fixture_name, dest_dir)" >&2
        return 1
    fi

    local e2e_dir
    e2e_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
    local fixture_src="${e2e_dir}/fixtures/${fixture_name}"

    if [[ ! -d "$fixture_src" ]]; then
        echo "[ERROR] kb_copy_fixture: fixture not found: ${fixture_src}" >&2
        return 1
    fi

    mkdir -p "$dest_dir"
    cp -r "${fixture_src}/." "${dest_dir}/"
    echo "[kb] fixture '${fixture_name}' copied to ${dest_dir}" >&2
}
