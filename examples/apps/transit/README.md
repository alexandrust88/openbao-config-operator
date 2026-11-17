# Encrypt application data with OpenBao Transit

This small Go program is an application client, not a controller. The OpenBao Config
Operator configures the Transit keys, ACL policy and Kubernetes authentication role.
The application authenticates with its own projected ServiceAccount token and calls
Transit directly. It does not receive an administrator token or export a key.

The program uses only the Go standard library. Build it from the repository root:

```sh
go build -o bin/transit-example ./examples/apps/transit
export PATH="$PWD/bin:$PATH"
go test -race ./examples/apps/transit
```

## Workload configuration

Mount an OpenBao CA certificate and a projected ServiceAccount token whose audience
matches the operator-managed Kubernetes auth role. Configure:

| Environment variable | Meaning |
| --- | --- |
| `BAO_ADDR` | HTTPS OpenBao origin, for example `https://openbao-active.openbao.svc:8200` |
| `BAO_NAMESPACE` | OpenBao namespace; leave empty for root |
| `BAO_AUTH_MOUNT` | Kubernetes auth mount, default `kubernetes` |
| `BAO_AUTH_ROLE` | Kubernetes auth role bound to this workload's ServiceAccount and namespace |
| `BAO_TRANSIT_MOUNT` | Transit mount, default `transit` |
| `BAO_CA_FILE` | Mounted PEM CA file |
| `BAO_JWT_FILE` | Projected token file; defaults to the standard ServiceAccount token path |

Grant only the needed `transit/encrypt/<key>`, `transit/decrypt/<key>` and
`transit/rewrap/<key>` operations, plus `auth/token/revoke-self`. The latter lets
the program revoke its short-lived OpenBao session after each invocation. Omit
key-management, export and backup permissions. Migration additionally requires
source-key decrypt and destination-key encrypt permissions.

Each command reads one JSON object from standard input. Values never belong in
command-line flags or environment variables. Standard output is the command's
result, **not a safe log stream**: `decrypt` returns base64-encoded plaintext.
Keep result files private, and never run decrypt as a Job whose output you publish.
Base64 is transport encoding, not encryption.

## Encrypt and decrypt

These examples use deliberately non-secret probe data and the built binary on `PATH`.
Run with the workload configuration and mounted token and CA above. The lab container
uses `/transit` as its entrypoint, with the same arguments:

```sh
umask 077
printf '%s\n' '{"plaintext":"aGVsbG8="}' |
  transit-example encrypt --key app > encrypted.json
transit-example decrypt --key app < encrypted.json > decrypted.json
```

The encrypt result contains `ciphertext`; the decrypt result contains `plaintext`.
The application is responsible for persisting ciphertext and consuming plaintext.
Transit does not store application ciphertext on its behalf.

## Rotate a key version and rewrap

After the operator rotates the **same** Transit key, rewrap existing ciphertext:

```sh
transit-example rewrap --key app < encrypted.json > rewrapped.json
```

This calls `transit/rewrap/app`. OpenBao changes the ciphertext's key version without
returning plaintext to the application. Keep the old ciphertext until the new value
is durably stored and verified. Raising `minDecryptionVersion` too early can make
unmigrated ciphertext unreadable; key rotation alone does not rewrite stored data.

## Move data to a different key

Rewrap cannot move ciphertext from `app` to a different key such as `replacement`:

```sh
transit-example migrate-key --key app --destination-key replacement \
  < encrypted.json > migrated.json
```

The program decrypts with `app`, then encrypts that plaintext with `replacement`.
Only the replacement ciphertext is written to standard output. Plaintext therefore
exists inside this application's memory and trust boundary; Go does not guarantee
that all temporary string copies can be erased. This operation does not update a
database atomically or delete the source key. Verify and persist the replacement
before retiring the old key. Losing either a required key version or the only
ciphertext copy is not recoverable through this example.

This example uses direct Transit encryption. It does not implement envelope
encryption, derived keys, associated data, convergent encryption or streaming of
large payloads. Input and HTTP responses are capped at 1 MiB; commands have a
45-second work deadline, a separate three-second session-revocation grace period and
bounded HTTP requests. TLS verification is mandatory,
and redirects are rejected instead of forwarding the workload token.

## Safe end-to-end smoke command

The lab runs this command as a Kubernetes Job:

```sh
transit-example smoke --key app --migration-key migration --denied-key other --denied-namespace lab-alpha
```

For a workload in a child namespace, use `--denied-namespace root` to probe root.
The command encrypts and decrypts an internal non-secret fixture, rewraps it under
the same key, migrates it to the allowed `migration` key, and verifies both results
by decrypting them. It then requires
HTTP **403** for the other key, the other namespace, key export and key backup.
A missing endpoint or unavailable server fails the test. It prints only
`{"ok":true}` on success and revokes its session. The lab must create the positive
and negative fixtures and policy bindings before running it.

See the official [Transit API](https://openbao.org/api-docs/secret/transit/) for
rewrap semantics and the [Kubernetes auth API](https://openbao.org/api-docs/auth/kubernetes/)
for workload login parameters.
