The connection needs access to an existing Transit mount. The operator creates keys at
`<mount>/keys/<name>` and updates mutable settings at `<mount>/keys/<name>/config`.

- **Irreversible settings:** once enabled, `exportable` and `allowPlaintextBackup` cannot be disabled.
- **Rotation:** `autoRotatePeriod` schedules rotation in OpenBao; changing `rotateNonce` requests one
  rotation. Rotation creates a new version while preserving older key material. Raising
  `minDecryptionVersion` can make older ciphertext undecryptable until that limit is lowered.
- **Deletion:** `deletionAllowed` enables deletion in OpenBao; it does not request deletion by the
  operator. Operator deletion also needs `deletionPolicy: Delete` and
  `obco.dev/allow-destructive-delete: "true"`. The default `Orphan` retains the key.
  Deleting its parent mount or OpenBao namespace still destroys the contained key material.
- **Status:** Ready means the managed configuration matches the current spec. It does not prove an
  application's token can encrypt or decrypt; verify that with the application's identity.
- **Permissions:** grant read/create/update on the key paths, and delete only when deletion is
  intended. Explicit rotation also needs update on `<mount>/keys/<name>/rotate`.
