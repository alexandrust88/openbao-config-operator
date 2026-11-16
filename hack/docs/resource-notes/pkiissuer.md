Create the PKI mount before the issuer. Root generation, intermediate signing and importing an
existing certificate are different workflows; choose the issuer type and key source explicitly.

An internally generated private key stays in OpenBao. With an exported key, configure the output
Secret before creation: private-key delivery is part of successful reconciliation. Losing a CR's
status must not be treated as permission to generate a replacement CA.

The default `Orphan` keeps the issuer when its CR is deleted. `Delete` also requires the
destructive-delete annotation. Deleting a parent mount or OpenBao namespace destroys its CA
material regardless of the issuer's retention policy.

Ready confirms the issuer workflow and managed configuration; it does not prove an application
trusts the chain. Verify certificate issuance and trust separately. Grant permissions only for the
chosen generation/import, issuer configuration and signing paths; a signer may need a separate
connection. See the PKI guide and the operator policy reference for the full workflow.
