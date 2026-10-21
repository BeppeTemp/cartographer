#!/usr/bin/env bash
# The sync scenarios against a real forge (D996): Forgejo or GitLab CE in a
# container, the token over HTTPS, a header-bearing write, a header-less one,
# sync_pull from a second clone, a protected branch, and (Forgejo) SSH.
#
#   test/forge/run.sh forgejo|gitlab       needs docker, curl, python3, git, make
#
# Not part of `make gate`, `make e2e` or an ordinary PR: CI runs it on the
# release PR, on a PR that touches test/forge/ and on workflow_dispatch (job
# `forge`); by hand, run it from the repository root. FORGE_KEEP=1 leaves the
# container up. Pinned images, never `latest`; bump them here.
set -euo pipefail

FORGE=${1:-}
FORGEJO_IMAGE=${FORGEJO_IMAGE:-codeberg.org/forgejo/forgejo:15}
GITLAB_IMAGE=${GITLAB_IMAGE:-gitlab/gitlab-ce:18.11.12-ce.0}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN="$ROOT/bin/cartographer"
[ -x "$BIN" ] || make -C "$ROOT" build
WORK="$(mktemp -d "${TMPDIR:-/tmp}/cartographer_forge_XXXXXX")"
CONTAINER="cartographer-forge-$FORGE-$$"
PORT=${FORGE_PORT:-18930}        # the forge's HTTP port on 127.0.0.1
SSH_PORT=${FORGE_SSH_PORT:-18931} # Forgejo's SSH port
SRV_PORT=${FORGE_SERVER_PORT:-18932}
SRV_PID=""
FAILURES=0

# No git identity on this host, as in test/e2e/run.sh: what the server commits
# as must come from the server, never from the runner's config.
export GIT_CONFIG_GLOBAL="$WORK/gitconfig" GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0
printf '[init]\n\tdefaultBranch = main\n' > "$GIT_CONFIG_GLOBAL"

pass() { echo "[PASS] $1"; }
fail() { echo "[FAIL] $1"; FAILURES=$((FAILURES + 1)); }
check() { # check DESCRIPTION CONDITION-COMMAND...
  local d=$1; shift
  if "$@"; then pass "$d"; else fail "$d"; fi
}

cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null && wait "$SRV_PID" 2>/dev/null || true
  if [ "$FAILURES" -gt 0 ] || [ -n "${FORGE_LOGS:-}" ]; then
    docker logs --tail 80 "$CONTAINER" 2>&1 | sed 's/^/[forge] /' || true
  fi
  [ -n "${FORGE_KEEP:-}" ] && { echo "[forge] container $CONTAINER kept; work dir $WORK"; return; }
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

case "$FORGE" in
  forgejo|gitlab) ;;
  *) echo "usage: $0 forgejo|gitlab" >&2; exit 2 ;;
esac
for tool in docker curl python3 git; do
  command -v "$tool" >/dev/null || { echo "missing $tool" >&2; exit 2; }
done

BASE="http://127.0.0.1:$PORT"
USERNAME=ada
EMAIL=ada@example.test
PASSWORD='Passw0rd-for-tests-only'
ADA='Ada Example <ada@example.test>'

json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }

wait_for() { # wait_for DESCRIPTION SECONDS COMMAND...
  local what=$1 limit=$2; shift 2
  local t=0
  until "$@" >/dev/null 2>&1; do
    t=$((t + 3)); sleep 3
    [ "$t" -lt "$limit" ] || { echo "[ERROR] timed out waiting for $what" >&2; docker logs --tail 40 "$CONTAINER" >&2 || true; exit 1; }
  done
  echo "[forge] $what ready after ~${t}s"
}

# --- the forge: container, user, access token, empty project ---------------
start_forgejo() {
  docker run -d --name "$CONTAINER" -p "127.0.0.1:$PORT:3000" -p "127.0.0.1:$SSH_PORT:22" \
    -e FORGEJO__security__INSTALL_LOCK=true \
    -e FORGEJO__server__ROOT_URL="$BASE/" \
    -e FORGEJO__server__DOMAIN=127.0.0.1 \
    -e FORGEJO__server__SSH_DOMAIN=127.0.0.1 \
    -e FORGEJO__server__SSH_PORT="$SSH_PORT" \
    -e FORGEJO__database__DB_TYPE=sqlite3 \
    -e FORGEJO__service__DISABLE_REGISTRATION=true \
    -e FORGEJO__log__LEVEL=Warn \
    "$FORGEJO_IMAGE" >/dev/null
  wait_for "Forgejo" 300 curl -sf "$BASE/api/healthz"
  # The first account cannot come from the API (registration is closed and an
  # empty instance has no admin): the one place the CLI is used.
  docker exec -u 1000 "$CONTAINER" forgejo admin user create --admin --username "$USERNAME" \
    --password "$PASSWORD" --email "$EMAIL" --must-change-password=false >/dev/null
  TOKEN=$(curl -sf -u "$USERNAME:$PASSWORD" -X POST "$BASE/api/v1/users/$USERNAME/tokens" \
    -H 'content-type: application/json' -d '{"name":"ci","scopes":["all"]}' | json 'd["sha1"]')
  curl -sf -X POST "$BASE/api/v1/user/repos" -H "Authorization: token $TOKEN" -H 'content-type: application/json' \
    -d '{"name":"kb-rules","private":true,"auto_init":false,"default_branch":"main"}' >/dev/null
  REMOTE="$BASE/$USERNAME/kb-rules.git"
  api() { # api METHOD PATH [JSON]
    curl -s -X "$1" "$BASE/api/v1$2" -H "Authorization: token $TOKEN" -H 'content-type: application/json' ${3:+-d "$3"}
  }
  # remote_commit_field FIELD -> the newest commit on main, author|committer email
  remote_commit() {
    api GET "/repos/$USERNAME/${1:-kb-rules}/commits?sha=main&limit=1&stat=false&verification=false&files=false" \
      | json "d[0]['commit']['author']['email'] + ' ' + d[0]['commit']['committer']['email'] + ' ' + d[0]['sha']"
  }
  protect_main() {
    api POST "/repos/$USERNAME/kb-rules/branch_protections" \
      '{"branch_name":"main","rule_name":"main","enable_push":false,"enable_push_whitelist":false,"apply_to_admins":true}' >/dev/null
  }
  add_ssh_key() { api POST "/user/keys" "{\"title\":\"ci\",\"key\":\"$(cat "$1")\"}" >/dev/null; }
  SSH_REMOTE="ssh://git@127.0.0.1:$SSH_PORT/$USERNAME/kb-rules.git"
}

start_gitlab() {
  docker run -d --name "$CONTAINER" --shm-size 256m -p "127.0.0.1:$PORT:$PORT" \
    -e GITLAB_ROOT_PASSWORD="$PASSWORD" \
    -e GITLAB_OMNIBUS_CONFIG="external_url '$BASE'; nginx['listen_port']=$PORT; nginx['listen_https']=false; puma['worker_processes']=0; sidekiq['concurrency']=4; prometheus_monitoring['enable']=false; gitlab_rails['gitlab_signup_enabled']=false" \
    "$GITLAB_IMAGE" >/dev/null
  # Booting takes minutes. /-/readiness is 404 for a client outside the
  # instance's monitoring allowlist (the docker bridge is), so the sign-in page
  # answering 200 is the sign that Rails serves.
  wait_for "GitLab CE" 900 curl -sf -o /dev/null "$BASE/users/sign_in"
  # A personal access token with a known value, made in the instance itself:
  # the API cannot mint the first one.
  TOKEN="glpat-ci$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
  docker exec "$CONTAINER" gitlab-rails runner "
    u = User.find_by_username('root'); u.update!(email: '$EMAIL', skip_reconfirmation: true) rescue nil
    t = u.personal_access_tokens.create!(name: 'ci', scopes: [:api, :write_repository, :read_repository], expires_at: 30.days.from_now)
    t.set_token('$TOKEN'); t.save!" >/dev/null
  USERNAME=root
  api() {
    curl -s -X "$1" "$BASE/api/v4$2" -H "PRIVATE-TOKEN: $TOKEN" -H 'content-type: application/json' ${3:+-d "$3"}
  }
  PID=$(api POST /projects '{"name":"kb-rules","visibility":"private","initialize_with_readme":false,"default_branch":"main"}' | json 'd["id"]')
  REMOTE="$BASE/root/kb-rules.git"
  remote_commit() {
    api GET "/projects/$PID/repository/commits?ref_name=main&per_page=1" \
      | json "d[0]['author_email'] + ' ' + d[0]['committer_email'] + ' ' + d[0]['id']"
  }
  protect_main() { # nobody pushes to main
    api DELETE "/projects/$PID/protected_branches/main" >/dev/null
    api POST "/projects/$PID/protected_branches?name=main&push_access_level=0&merge_access_level=40" >/dev/null
  }
  SSH_REMOTE=""
  # The membership rule is a push rule, a Premium feature: CE answers 404/403.
  RULE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v4/projects/$PID/push_rule" \
    -H "PRIVATE-TOKEN: $TOKEN" -d 'member_check=true' || true)
  if [ "$RULE" = 200 ] || [ "$RULE" = 201 ]; then MEMBER_RULE=1; else
    MEMBER_RULE=""; echo "[forge] push rules are not in this GitLab edition (HTTP $RULE): the member check is covered by test/forge/pre-receive.sh in the Go tests and scenario 21"
  fi
}

MEMBER_RULE=${MEMBER_RULE:-}
"start_$FORGE"
echo "[forge] $FORGE up, project $REMOTE"

# --- the server on a KB created against that remote ---------------------------
CRED="$WORK/credentials"; DATA="$WORK/data"
mkdir -p "$CRED" "$DATA"
printf '%s' "$TOKEN" > "$CRED/kb-rules.token"; chmod 600 "$CRED/kb-rules.token"
export CARTOGRAPHER_CREDENTIALS_DIR="$CRED" CARTOGRAPHER_AUDIT_LOG="$WORK/audit/audit.log"
# `kb create --remote` pushes with the operator's own git credentials: it does not
# read the credentials directory, which the server does. A helper stands for them.
operator_git() {
  GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=credential.helper \
    GIT_CONFIG_VALUE_0="!f() { echo username=token; echo password=$TOKEN; }; f" "$@"
}
operator_git "$BIN" kb create kb-rules --remote "$REMOTE" --data "$DATA" --local >"$WORK/kb-create.log" 2>&1 \
  || { cat "$WORK/kb-create.log"; fail "kb create against the forge over HTTPS"; exit 1; }
pass "kb create against the forge over HTTPS"
KB="$DATA/kb-rules"

MCP="http://127.0.0.1:$SRV_PORT/mcp?kb=kb-rules"
K_SERVICE=forge CARTOGRAPHER_AUTH=false "$BIN" serve --data "$DATA" --http "127.0.0.1:$SRV_PORT" >"$WORK/serve.log" 2>&1 &
SRV_PID=$!
wait_for "the server" 60 bash -c "curl -sf http://127.0.0.1:$SRV_PORT/health | grep -q '\"kbs\"' && ! curl -sf http://127.0.0.1:$SRV_PORT/health | grep -q bootstrapping"

call() { # call TOOL ARGS [AUTHOR] -> the raw JSON-RPC response
  local h=(); [ -n "${3:-}" ] && h=(-H "Cartographer-Author: $3")
  curl -s -X POST "$MCP" ${h[@]+"${h[@]}"} -H 'content-type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}"
}
page() { # page NAME BODY [AUTHOR]: an Index page and the note it links (write gate, D907)
  call concept_batch "{\"operations\":[{\"op\":\"write\",\"id\":\"rules/$1\",\"index_entry\":true,\"frontmatter\":{\"type\":\"Index\",\"title\":\"$1\"},\"body\":\"# $1\\n\\nSee [[rules/$1/note]].\\n\"},{\"op\":\"write\",\"id\":\"rules/$1/note\",\"index_entry\":true,\"frontmatter\":{\"type\":\"note\",\"title\":\"$1 note\"},\"body\":\"$2\"}]}" "${3:-}"
}
contains() { printf '%s' "$1" | grep -q "$2"; }
head_is() { [ "$(remote_commit)" = "$1" ]; }
no_warning() { ! printf '%s' "$1" | grep -q sync_state; }

call map_create '{"name":"rules","title":"Rules"}' "$ADA" >/dev/null

echo "--- a write that names its author ---"
OUT=$(page one "By a header." "$ADA")
check "the push lands (no sync warning)" no_warning "$OUT"
read -r AE CE SHA_A <<<"$(remote_commit)"
check "the forge shows the header's author and committer" [ "$AE $CE" = "$EMAIL $EMAIL" ]

echo "--- a write with no header ---"
OUT=$(page two "By the token.")
if [ -n "$MEMBER_RULE" ]; then
  check "the member rule refuses the token's identity" contains "$OUT" sync_state
else
  check "the push lands (no sync warning)" no_warning "$OUT"
  read -r AE CE SHA_B <<<"$(remote_commit)"
  check "the forge shows the server's token-named author" [ "$AE" != "$EMAIL" ]
fi

echo "--- a second clone publishes; the next write pulls it in ---"
SECOND="$WORK/second"
operator_git git clone -q "$REMOTE" "$SECOND"
(
  cd "$SECOND"
  mkdir -p data/rules && printf 'From a second clone.\n' > data/rules/second.md
  git add -A && git -c user.name="Bo Example" -c user.email=bo@example.test commit -q -m "second clone"
  operator_git git push -q origin HEAD:main
) || fail "the second clone could not push"
# (sync_pull is the provisioning manifest tool: the git pull is the sync-in that
# precedes every write.)
OUT=$(page four "After the second clone." "$ADA")
check "the write after the pull lands" no_warning "$OUT"
check "the second clone's file was pulled in" [ -f "$KB/data/rules/second.md" ]
read -r AE CE SHA_D <<<"$(remote_commit)"
check "the forge's head is the new write, on top of the second clone's" [ "$AE $CE" = "$EMAIL $EMAIL" ]

echo "--- a protected branch ---"
BEFORE=$(remote_commit)
protect_main
OUT=$(page three "Behind a protected branch." "$ADA")
check "the write reports the protected branch" contains "$OUT" protected
check "the remote did not move" head_is "$BEFORE"
check "the change is kept in the clone" [ -f "$KB/data/rules/three/note.md" ]

if [ -n "$SSH_REMOTE" ]; then
  echo "--- SSH (Forgejo): the KB's own key, <kb>.ssh ---"
  ssh-keygen -q -t ed25519 -N '' -f "$WORK/id" -C ci
  add_ssh_key "$WORK/id.pub"
  ssh-keyscan -t ed25519 -p "$SSH_PORT" 127.0.0.1 > "$CRED/known_hosts" 2>/dev/null
  cp "$WORK/id" "$CRED/kb-ssh.ssh"; chmod 600 "$CRED/kb-ssh.ssh"
  api POST /user/repos '{"name":"kb-ssh","private":true,"auto_init":false,"default_branch":"main"}' >/dev/null
  # kb create pushes with the operator's ssh, as with the token above.
  if GIT_SSH_COMMAND="ssh -i '$CRED/kb-ssh.ssh' -o UserKnownHostsFile='$CRED/known_hosts' -o StrictHostKeyChecking=yes" \
      "$BIN" kb create kb-ssh --remote "ssh://git@127.0.0.1:$SSH_PORT/$USERNAME/kb-ssh.git" --data "$DATA" --local >"$WORK/kb-ssh.log" 2>&1; then
    pass "kb create against the forge over SSH"
  else
    cat "$WORK/kb-ssh.log"; fail "kb create against the forge over SSH"
  fi
  # The server mounts it at its next start, and writes with <kb>.ssh alone.
  kill "$SRV_PID"; wait "$SRV_PID" 2>/dev/null || true
  K_SERVICE=forge CARTOGRAPHER_AUTH=false "$BIN" serve --data "$DATA" --http "127.0.0.1:$SRV_PORT" >"$WORK/serve2.log" 2>&1 &
  SRV_PID=$!
  wait_for "the server" 60 bash -c "curl -sf http://127.0.0.1:$SRV_PORT/health | grep -q '\"kbs\"' && ! curl -sf http://127.0.0.1:$SRV_PORT/health | grep -q bootstrapping"
  MCP="http://127.0.0.1:$SRV_PORT/mcp?kb=kb-ssh"
  call map_create '{"name":"rules","title":"Rules"}' "$ADA" >/dev/null
  OUT=$(page one "Over SSH." "$ADA")
  check "a write over SSH lands" no_warning "$OUT"
  read -r AE CE SHA_S <<<"$(remote_commit kb-ssh)"
  check "the forge shows the header's author over SSH" [ "$AE $CE" = "$EMAIL $EMAIL" ]
fi

echo
if [ "$FAILURES" -eq 0 ]; then echo "[forge $FORGE] PASS"; else echo "[forge $FORGE] FAIL ($FAILURES)"; exit 1; fi
