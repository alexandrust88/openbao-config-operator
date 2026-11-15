#!/usr/bin/env bash
# Builds examples-<version>.tar.gz, the release asset with the curated examples (examples/) and the generated samples
# (examples-generated/) of this release, at the same paths as in the repository. Only files git tracks, or would
# track, are packed, so ignored local files (examples/live/params/params.env, keys, kubeconfigs) never ship.
# @VERSION@ and @TAG@ in a packed file become this release's version and tag, as on the docs site.
# The archive is reproducible: sorted entries, mtime SOURCE_DATE_EPOCH (default: the last commit), owner 0:0,
# modes 0644/0755, ustar format, gzip without name or timestamp. Prints the archive path.
# Usage: hack/release/examples-bundle.sh <version> <out-dir>
set -euo pipefail

[ $# -eq 2 ] || { echo "usage: $0 <version> <out-dir>" >&2; exit 2; }
version=${1#v}
"$(dirname "$0")/check-version.sh" "v$version" || exit 2
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$2"
out=$(cd "$2" && pwd)/examples-$version.tar.gz

epoch=${SOURCE_DATE_EPOCH:-$(git -C "$repo_root" log -1 --format=%ct)}
stamp=$(date -u -r "$epoch" +%Y%m%d%H%M.%S 2>/dev/null || date -u -d "@$epoch" +%Y%m%d%H%M.%S)

stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT
mkdir "$stage/root"

files=()
while IFS= read -r -d '' f; do
  if [ -f "$repo_root/$f" ]; then files+=("$f"); fi
done < <(git -C "$repo_root" ls-files -z --cached --others --exclude-standard -- examples examples-generated |
  LC_ALL=C sort -z -u)
[ "${#files[@]}" -gt 0 ] || { echo "examples-bundle: no files under examples/ or examples-generated/" >&2; exit 1; }

for f in "${files[@]}"; do
  case ${f##*/} in
    .env* | *.env | *.pem | *.key | *.crt | *.cer | *.p12 | *.pfx | *.jks | *.keytab | *.kubeconfig | kubeconfig*)
      echo "examples-bundle: refusing to pack $f: credential-like file name" >&2
      exit 1
      ;;
  esac
  mkdir -p "$stage/root/$(dirname "$f")"
  if grep -q -e '@VERSION@' -e '@TAG@' "$repo_root/$f"; then
    sed -e "s/@VERSION@/$version/g" -e "s/@TAG@/v$version/g" "$repo_root/$f" >"$stage/root/$f"
  else
    cp "$repo_root/$f" "$stage/root/$f"
  fi
  if [ -x "$repo_root/$f" ]; then chmod 0755 "$stage/root/$f"; else chmod 0644 "$stage/root/$f"; fi
  TZ=UTC touch -t "$stamp" "$stage/root/$f"
done

printf '%s\0' "${files[@]}" >"$stage/list"
# GNU tar on the release runners; bsdtar (macOS) for a local dry run. Each is reproducible on its own.
case "$(tar --version 2>/dev/null)" in
  *'GNU tar'*) owner=(--owner=0 --group=0 --numeric-owner) ;;
  *) owner=(--uid 0 --gid 0 --uname root --gname root --no-xattrs --no-acls) ;;
esac
COPYFILE_DISABLE=1 tar -c -f - -C "$stage/root" --format=ustar "${owner[@]}" --null -T "$stage/list" |
  gzip -n -9 >"$out"
echo "$out"
