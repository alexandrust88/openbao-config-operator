The Kubernetes auth mount and its TokenReview configuration must exist before workloads can log
in. This role binds Kubernetes ServiceAccount names and namespaces to OpenBao policies; creating
the role does not create those policies or ServiceAccounts.

Use an audience dedicated to OpenBao and configure clients to request that audience. Namespace
selectors resolve Kubernetes namespaces; they do not select OpenBao namespaces. The connection
and the resource's OpenBao namespace setting determine the target in OpenBao.

Ready confirms that the role matches its spec. It does not validate TokenReview connectivity or a
workload login. Verify authentication using a projected token from the intended ServiceAccount.
Grant read/create/update on `auth/<mount>/role/<name>`, plus delete when explicitly needed.
The default `Orphan` retains the role; retaining a role can retain access and requires an explicit
access-revocation process.
