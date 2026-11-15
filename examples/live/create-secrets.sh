#!/usr/bin/env bash
# Creates the input Secrets that the live kit components reference, with generated values, in one Kubernetes
# namespace. A Secret that already exists is left as it is, so every overlay shares the same values.
set -euo pipefail

usage() {
  cat <<'EOF'
usage: create-secrets.sh [--namespace NAME] [--list] [COMPONENTS_DIR...]

  --namespace  Kubernetes namespace of the Secrets (default: kubernetesNamespace in params/kustomization.yaml)
  --list       print the Secret names and keys with their generators, create nothing

COMPONENTS_DIR defaults to the components directory next to this script. Every input-secrets.txt below it is
read; each line is "<secret> <key> <generator>". Generators:
  password          32 random letters and digits
  literal=<value>   the value itself (non-secret settings such as a user name)
  base32            a random 20-byte key, base32 encoded (TOTP seeds)
  rsa-key           a 2048-bit RSA private key, PKCS#8 PEM
  rsa-key-pkcs1     a 2048-bit RSA private key, PKCS#1 PEM
  ec-key            a P-256 EC private key, SEC1 PEM
  ssh-key           a 2048-bit RSA private key in PEM form for SSH
  pem-bundle        a client certificate and its RSA private key, then the self-signed CA certificate that issued it
  pem-json          the same as pem-bundle, as JSON {"certificate": ..., "private_key": ..., "issuing_ca": ...}
  ca-cert           a self-signed CA certificate only (its key is discarded)
  kv-json=<key>     a KV version 2 write body {"data":{"<key>":"<32 random letters and digits>"}}
  bcrypt=<key>      the bcrypt hash of the value generated for <key> of the same Secret (needs htpasswd)
  ca-cert-for=<key> a self-signed CA certificate of the private key generated for <key> of the same Secret

Values are never printed. Needs kubectl and openssl; ssh-key needs ssh-keygen, bcrypt needs htpasswd, base32 and
pem-json need python3.
EOF
}

err() { printf '%s: %s\n' "${0##*/}" "$*" >&2; }

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
namespace=""
list=false
dirs=()
while [ $# -gt 0 ]; do
  case $1 in
    --namespace) namespace=${2:?--namespace needs a value}; shift ;;
    --list) list=true ;;
    -h | --help) usage; exit 0 ;;
    -*) err "unknown argument: $1"; usage >&2; exit 2 ;;
    *) dirs+=("$1") ;;
  esac
  shift
done
if [ "${#dirs[@]}" -eq 0 ]; then dirs=("$script_dir/components"); fi
if [ -z "$namespace" ]; then
  namespace=$(sed -n 's/^ *- kubernetesNamespace=//p' "$script_dir/params/kustomization.yaml")
fi

entries=$(find "${dirs[@]}" -name input-secrets.txt -type f -print0 | xargs -0 cat | sed -e 's/#.*//' -e '/^[[:space:]]*$/d' | sort -u)
if [ -z "$entries" ]; then
  err "no input-secrets.txt below ${dirs[*]}"
  exit 1
fi
if $list; then
  printf '%s\n' "$entries"
  exit 0
fi

for tool in kubectl openssl; do
  if ! command -v "$tool" >/dev/null; then
    err "$tool not found in PATH"
    exit 1
  fi
done

work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT
umask 077

# Writes <out>.crt, a client certificate for the new RSA key <out>.key, and <out>.ca.crt, the throwaway CA that
# issued it. The files stay in work_dir, which the exit trap removes.
client_cert() {
  local out=$1
  openssl req -x509 -newkey rsa:2048 -nodes -days 365 -subj "/CN=openbao live kit test CA" \
    -addext basicConstraints=critical,CA:true -addext keyUsage=critical,keyCertSign,cRLSign \
    -keyout "$out.ca.key" -out "$out.ca.crt" 2>/dev/null &&
    openssl req -new -newkey rsa:2048 -nodes -subj "/CN=openbao live kit client" -keyout "$out.key" \
      -out "$out.csr" 2>/dev/null &&
    openssl x509 -req -sha256 -days 365 -in "$out.csr" -CA "$out.ca.crt" -CAkey "$out.ca.key" \
      -set_serial "0x$(openssl rand -hex 16)" -out "$out.crt" 2>/dev/null \
      -extfile <(printf 'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=clientAuth\n')
}

generate() {
  local generator=$1 out=$2 dir=$3 source=""
  case $generator in
    bcrypt=* | ca-cert-for=*)
      source="$dir/${generator#*=}"
      if [ ! -s "$source" ]; then
        err "$generator: list key ${generator#*=} of the same Secret in input-secrets.txt"
        return 1
      fi
      ;;
  esac
  case $generator in
    password) openssl rand -base64 64 | LC_ALL=C tr -dc 'A-Za-z0-9' | head -c 32 >"$out" ;;
    literal=*) printf '%s' "${generator#literal=}" >"$out" ;;
    base32) openssl rand 20 | python3 -c 'import base64,sys; sys.stdout.write(base64.b32encode(sys.stdin.buffer.read()).decode())' >"$out" ;;
    rsa-key) openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$out" 2>/dev/null ;;
    rsa-key-pkcs1) openssl genrsa -traditional -out "$out" 2048 2>/dev/null ;;
    ec-key) openssl ecparam -name prime256v1 -genkey -noout -out "$out" 2>/dev/null ;;
    ssh-key) ssh-keygen -q -t rsa -b 2048 -m PEM -N '' -C live-kit -f "$out.ssh" && mv "$out.ssh" "$out" && rm -f "$out.ssh.pub" ;;
    pem-bundle) client_cert "$out" && cat "$out.crt" "$out.key" "$out.ca.crt" >"$out" ;;
    pem-json)
      client_cert "$out" &&
        python3 -c 'import json, sys; c, k, a = (open(f).read() for f in sys.argv[1:]); sys.stdout.write(json.dumps({"certificate": c, "private_key": k, "issuing_ca": a}))' \
          "$out.crt" "$out.key" "$out.ca.crt" >"$out"
      ;;
    ca-cert)
      openssl req -x509 -newkey rsa:2048 -nodes -days 365 -subj "/CN=openbao live kit test CA" \
        -addext basicConstraints=critical,CA:true -keyout /dev/null -out "$out" 2>/dev/null
      ;;
    kv-json=*)
      printf '{"data":{"%s":"%s"}}' "${generator#kv-json=}" \
        "$(openssl rand -base64 64 | LC_ALL=C tr -dc 'A-Za-z0-9' | head -c 32)" >"$out"
      ;;
    ca-cert-for=*)
      openssl req -x509 -new -key "$source" -days 365 -subj "/CN=openbao live kit test CA" \
        -addext basicConstraints=critical,CA:true -addext keyUsage=critical,keyCertSign,cRLSign -out "$out" 2>/dev/null
      ;;
    bcrypt=*)
      if ! command -v htpasswd >/dev/null; then
        err "htpasswd not found in PATH (apache2-utils); needed for bcrypt"
        return 1
      fi
      htpasswd -niBC 10 live <"$source" | cut -d: -f2- | tr -d '\n' >"$out"
      ;;
    *) err "unknown generator: $generator"; return 1 ;;
  esac
}

failed=false
for secret in $(printf '%s\n' "$entries" | awk '{print $1}' | sort -u); do
  if kubectl get secret "$secret" -n "$namespace" >/dev/null 2>&1; then
    printf 'exists  %s/%s\n' "$namespace" "$secret"
    continue
  fi
  dir="$work_dir/$secret"
  mkdir -p "$dir"
  args=()
  while read -r name key generator; do
    [ "$name" = "$secret" ] || continue
    if ! generate "$generator" "$dir/$key" "$dir"; then
      failed=true
      continue 2
    fi
    args+=("--from-file=$key=$dir/$key")
  done <<<"$(printf '%s\n' "$entries" | awk -v s="$secret" '$1 == s && $3 !~ /^(bcrypt|ca-cert-for)=/' ; printf '%s\n' "$entries" | awk -v s="$secret" '$1 == s && $3 ~ /^(bcrypt|ca-cert-for)=/')"
  kubectl create secret generic "$secret" -n "$namespace" "${args[@]}" >/dev/null
  kubectl label secret "$secret" -n "$namespace" app.kubernetes.io/part-of=openbao-live-kit >/dev/null
  printf 'created %s/%s\n' "$namespace" "$secret"
done
if $failed; then exit 1; fi
