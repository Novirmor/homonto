#!/usr/bin/env bash
# Suite: hosting — the hosting workflows against a REAL sandbox repository on
# one host ($E2E_HOST: github | gitlab | bitbucket). Run by
# scripts/hosting-sandbox.sh, which passes the sandbox fixtures and credentials;
# never by CI. Provider differences: docs/guides/hosting-differences.md.
#
# Layers:
#   1. runtime — the projected plugin shim drives `homonto runtime serve`
#      against the host: snapshot, stage, native approval, publish, reconcile,
#      cleanup (deterministic; proves every provider claim the runtime uses).
#   2. processes — each /h-* command through real OpenCode. Read-only
#      processes must end at a shown draft with nothing published (headless
#      runs have no native question dialog, so nothing may be approved).
#      Write processes (continue, resolve conflicts, resolve issue) run only
#      with HOSTING_WRITE=1 and reset their fixtures afterwards.
set -uo pipefail
source /opt/e2e-suites/lib.sh

HOST="${E2E_HOST:?E2E_HOST required (github|gitlab|bitbucket)}"
MODEL="${E2E_OPENCODE_MODEL:-opencode/north-mini-code-free}"
: "${HOSTING_REPO_URL:?clone URL of the sandbox repository}"
: "${HOSTING_PR_URL:?URL of the fixture pull/merge request}"
case "$HOST" in
  github) pkg=h-workflow-gh ;;
  gitlab) pkg=h-workflow-gl ;;
  bitbucket) pkg=h-workflow-bb ;;
  *) fail "unknown E2E_HOST: $HOST" ;;
esac

log "$HOST: authentication"
case "$HOST" in
  github) gh auth status >/dev/null 2>&1 || fail "gh is not authenticated (GH_TOKEN)" ;;
  gitlab) glab auth status --hostname gitlab.com >/dev/null 2>&1 || fail "glab is not authenticated (GITLAB_TOKEN)" ;;
  bitbucket) homonto host bitbucket auth status || fail "Bitbucket token rejected (HOMONTO_BITBUCKET_TOKEN)" ;;
esac
pass "$HOST credentials accepted"

log "$HOST: sandbox workspace"
W=/work
mkdir -p "$W" && cd "$W" || fail "workspace"
git clone --quiet "$HOSTING_REPO_URL" sandbox || fail "clone $HOSTING_REPO_URL"
git -C sandbox config user.email e2e@homonto.invalid && git -C sandbox config user.name "homonto e2e"
cat >homonto.toml <<EOF
schema_version = 2
[workflow]
root = "records"
git = "existing"
[repos]
sandbox = "sandbox"
[workflows.$pkg]
source = "builtin:$pkg"
scope = "project"
[engines.no]
source = "builtin:no"
scope = "project"
[subagents.homonto.opencode]
model = "$MODEL"
[subagents.h-spike.opencode]
model = "$MODEL"
[subagents.h-review.opencode]
model = "$MODEL"
[subagents.no-implementer.opencode]
model = "$MODEL"
[subagents.no-reviewer.opencode]
model = "$MODEL"
EOF
git init --quiet . 2>/dev/null || true
homonto apply >apply.log 2>&1 || { cat apply.log; fail "homonto apply"; }
binding=".homonto/catalog/plugins/homonto-workflow/binding.json"
jq -e ".${HOST}Enabled == true" "$binding" >/dev/null || fail "binding does not enable $HOST: $(cat "$binding")"
pass "$pkg applied; binding enables $HOST"

log "$HOST: runtime against the real host"
out="$(node /opt/e2e-hosting/runtime.mjs "$PWD/.homonto/catalog/plugins/homonto-workflow/github.ts" "$PWD/homonto.toml" "$PWD/sandbox" 2>&1)"
printf '%s\n' "$out"
contains "$out" "RUNTIME PASS: $HOST" "hosting runtime failed on $HOST"
pass "draft → approval → publish → reconcile → cleanup on $HOST"

# remote_comments prints the fixture PR's comment count (top-level and inline).
remote_comments() {
  case "$HOST" in
    github)
      local m; m="$(sed -E 's#^https://([^/]+)/([^/]+/[^/]+)/pull/([0-9]+)$#\1 \2 \3#' <<<"$HOSTING_PR_URL")"
      set -- $m
      { gh api --hostname "$1" "repos/$2/issues/$3/comments" --paginate --jq length
        gh api --hostname "$1" "repos/$2/pulls/$3/reviews" --paginate --jq length; } | awk '{s+=$1} END {print s+0}' ;;
    gitlab)
      local p iid; p="$(sed -E 's#^https://gitlab.com/(.+)/-/merge_requests/[0-9]+$#\1#' <<<"$HOSTING_PR_URL")"
      iid="${HOSTING_PR_URL##*/}"
      glab api --hostname gitlab.com --paginate "projects/$(jq -rn --arg p "$p" '$p|@uri')/merge_requests/$iid/notes?per_page=100" | jq '[.[] | select(.system == false)] | length' ;;
    bitbucket)
      local r n; r="$(sed -E 's#^https://bitbucket.org/([^/]+/[^/]+)/pull-requests/[0-9]+$#\1#' <<<"$HOSTING_PR_URL")"; n="${HOSTING_PR_URL##*/}"
      homonto host bitbucket api --paginate "repositories/$r/pullrequests/$n/comments?pagelen=50" | jq length ;;
  esac
}

[ -f "$HOME/.local/share/opencode/auth.json" ] || { printf '\nSUITE PASS (runtime only; no OpenCode auth mounted): hosting/%s\n' "$HOST"; exit 0; }

process() { # <name> <arguments> <must-contain>
  local name="$1" args="$2" want="$3" before after out
  before="$(remote_comments)"
  log "$HOST: /$name"
  out="$(timeout 1800 opencode run --agent homonto -m "$MODEL" "/$name $args" 2>&1)"
  printf '%s\n' "$out" | tail -40
  after="$(remote_comments)"
  [ "$before" = "$after" ] || fail "/$name published without native approval ($before → $after comments)"
  grep -qi -- "$want" <<<"$out" || fail "/$name did not reach its endpoint (no \"$want\")"
  pass "/$name reached its endpoint; nothing published without approval"
}

if [ "$HOST" = bitbucket ]; then
  process h-spike-issue "sandbox: ${HOSTING_BRIEF:-Add a CONTRIBUTING.md that explains how to run the tests.}" "brief"
elif [ -n "${HOSTING_ISSUE_URL:-}" ]; then
  process h-spike-issue "$HOSTING_ISSUE_URL" "brief"
fi
process h-review-pr "$HOSTING_PR_URL" "draft"
process h-review-batch "${HOSTING_WEB_URL:?repository web URL} limit 2" "draft"

# open_prs prints the numbers of the sandbox's open pull/merge requests.
open_prs() {
  case "$HOST" in
    github) gh pr list --repo "${HOSTING_WEB_URL#https://}" --state open --json number --jq '.[].number' ;;
    gitlab) glab api --hostname gitlab.com --paginate "projects/$(jq -rn --arg p "${HOSTING_WEB_URL#https://gitlab.com/}" '$p|@uri')/merge_requests?state=opened&per_page=100" | jq '.[].iid' ;;
    bitbucket) homonto host bitbucket api --paginate "repositories/${HOSTING_WEB_URL#https://bitbucket.org/}/pullrequests?state=OPEN&pagelen=50" | jq '.[].id' ;;
  esac | sort -n
}

close_pr() { # <number>
  case "$HOST" in
    github) gh pr close "$1" --repo "${HOSTING_WEB_URL#https://}" --delete-branch >/dev/null ;;
    gitlab) glab api --hostname gitlab.com --method PUT "projects/$(jq -rn --arg p "${HOSTING_WEB_URL#https://gitlab.com/}" '$p|@uri')/merge_requests/$1" -f state_event=close >/dev/null ;;
    bitbucket) printf '{}' | homonto host bitbucket api --method POST "repositories/${HOSTING_WEB_URL#https://bitbucket.org/}/pullrequests/$1/decline" --input - >/dev/null ;;
  esac
}

write_process() { # <name> <arguments> <branch> <pinned head> — the process must push to <branch>; the fixture is reset after
  local name="$1" args="$2" branch="$3" pinned="$4" moved out
  log "$HOST: /$name (write)"
  out="$(timeout 2400 opencode run --agent homonto -m "$MODEL" "/$name $args" 2>&1)"; printf '%s\n' "$out" | tail -40
  git -C sandbox fetch --quiet origin "$branch" || fail "fetch $branch"
  moved="$(git -C sandbox rev-parse FETCH_HEAD)"
  git -C sandbox push --quiet --force origin "$pinned:refs/heads/$branch" || fail "reset fixture $branch"
  [ "$moved" != "$pinned" ] || fail "/$name pushed nothing to $branch"
  pass "/$name pushed to $branch (fixture reset to $pinned)"
}

if [ "${HOSTING_WRITE:-0}" = 1 ]; then
  write_process h-resolve-conflicts "${HOSTING_CONFLICT_PR_URL:?conflicting fixture PR}" "${HOSTING_CONFLICT_BRANCH:?}" "${HOSTING_CONFLICT_HEAD:?}"
  write_process h-continue-pr "$HOSTING_PR_URL" "${HOSTING_PR_BRANCH:?fixture PR head branch}" "${HOSTING_PR_HEAD:?its pinned head}"

  log "$HOST: /h-resolve-issue (write)"
  before="$(open_prs)"
  if [ "$HOST" = bitbucket ]; then target="sandbox: ${HOSTING_BRIEF:-Add a CONTRIBUTING.md that explains how to run the tests.}"; else target="${HOSTING_ISSUE_URL:?fixture issue}"; fi
  out="$(timeout 3600 opencode run --agent homonto -m "$MODEL" "/h-resolve-issue $target" 2>&1)"; printf '%s\n' "$out" | tail -40
  created="$(comm -13 <(printf '%s\n' "$before") <(open_prs))"
  for n in $created; do close_pr "$n"; done
  [ -n "$created" ] || fail "/h-resolve-issue opened no PR"
  pass "/h-resolve-issue delivered PR(s) $created (closed again)"
fi

printf '\nSUITE PASS: hosting/%s (differences: docs/guides/hosting-differences.md)\n' "$HOST"
