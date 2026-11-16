# External plugin fixture

This separate Go module builds the live kit's external secrets and authentication
plugins. It keeps the OpenBao SDK and its dependencies out of the operator module.
The module and checksum file pin the SDK; build with `-mod=readonly`.

```sh
CGO_ENABLED=0 go build -mod=readonly -trimpath -o /tmp/livekit-plugin .
```

Install identical copies as `openbao-plugin-secrets-live-kit` and
`openbao-plugin-auth-live-kit` in OpenBao's configured plugin directory. The
executable name chooses the backend type. The test harness computes the executable
checksum and places it in the rendered Plugin resources before applying them.

Both backends expose an authenticated `health` read returning the fixture identity
and backend type. The live kit checks that route after the mount becomes Ready,
proving that OpenBao started and called the external process. This covers binary
registration, plugin references and mounting; it does not implement a production
authentication method or test external service credentials.

The full auth example uses `oci: false`. OCI plugin distribution still requires a
registry and a matching server-side plugin declaration and is not covered by this
binary fixture. Database plugins use a separate protocol and remain explicitly
excluded from the shared live kit.
