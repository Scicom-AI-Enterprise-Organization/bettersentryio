#!/usr/bin/env bash
# Tag a release and push it.
#
# Pushing a vX.Y.Z tag is the ONLY thing that publishes an image
# (.github/workflows/build.yml, which triggers on tags: ["v*"]). It builds two
# independent images, the engine and the web UI, and only a semver tag is marked
# deployable — anything else is a build artefact.
#
# Usage:
#   ./release.sh                 # bump the patch; first ever release is v0.1.0
#   ./release.sh v0.2.0          # an explicit version
#   ./release.sh --minor         # bump the minor, reset patch
#   ./release.sh --dry-run       # show what would happen, change nothing
#
# It does not ask for confirmation. `--dry-run` is where you look before you leap,
# and the guards below are the real protection: a clean tree, on main, and a tag
# that does not already exist. A y/N prompt on top of those bought nothing and
# made the script unusable from anything without a terminal — `read` fails on a
# closed stdin, so a non-interactive run "aborted" rather than releasing.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# Only reached on a repo with no tags yet: v0.1.0 matches the version the binary
# already reports (`version` in cmd/bettersentryio/main.go, "0.1.0-dev"), so the
# first published image does not disagree with what it says about itself.
FIRST="v0.1.0"

dry=0; want=""; bump="patch"
for a in "$@"; do
  case "$a" in
    --dry-run|-n) dry=1 ;;
    --minor) bump="minor" ;;
    --major) bump="major" ;;
    v[0-9]*) want="$a" ;;
    *) echo "unknown argument: $a" >&2; exit 2 ;;
  esac
done

if [[ -n "$(git status --porcelain)" ]]; then
  echo "error: uncommitted changes — a tag must capture exactly what is in the repo" >&2
  exit 1
fi
branch=$(git rev-parse --abbrev-ref HEAD)
if [[ "$branch" != "main" ]]; then
  echo "error: on '$branch' — releases are tagged from main" >&2
  exit 1
fi

git fetch --tags --quiet || true
latest=$(git tag -l 'v[0-9]*.[0-9]*.[0-9]*' | sort -V | tail -1)

if [[ -n "$want" ]]; then
  next="$want"
elif [[ -z "$latest" ]]; then
  next="$FIRST"
else
  IFS=. read -r ma mi pa <<< "${latest#v}"
  case "$bump" in
    major) next="v$((ma+1)).0.0" ;;
    minor) next="v${ma}.$((mi+1)).0" ;;
    patch) next="v${ma}.${mi}.$((pa+1))" ;;
  esac
fi

if git rev-parse "$next" >/dev/null 2>&1; then
  echo "error: tag $next already exists" >&2
  exit 1
fi

subject=$(git log -1 --pretty=%s)
echo "  latest tag : ${latest:-<none>}"
echo "  next tag   : $next"
echo "  commit     : $(git rev-parse --short HEAD)  $subject"
# The tag is printed unstripped because that is literally what CI publishes:
# build.yml takes version="${GITHUB_REF_NAME}", so the image tag keeps the "v".
registry="865626945255.dkr.ecr.ap-southeast-5.amazonaws.com"
echo "  engine     : $registry/scicom/bettersentryio:$next"
echo "  web        : $registry/scicom/bettersentryio-web:$next"

if [[ $dry -eq 1 ]]; then
  echo "  (dry run — nothing done)"
  exit 0
fi

git tag -a "$next" -m "$next: $subject"
git push origin "$next"
echo "  pushed $next — watch: gh run watch"
