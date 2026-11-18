# Support

openbao-config-operator is a community project maintained by volunteers. There is no commercial support and no
service-level agreement; the channels below are answered on a best-effort basis.

## Where to ask

| you want to | go to |
|-------------|-------|
| ask how to do something, or get help with your setup | [GitHub Discussions, Q&A](https://github.com/alexandrust88/openbao-config-operator/discussions/categories/q-a) |
| discuss an idea before it becomes an issue | [GitHub Discussions, Ideas](https://github.com/alexandrust88/openbao-config-operator/discussions/categories/ideas) |
| report a bug | a [bug report](https://github.com/alexandrust88/openbao-config-operator/issues/new?template=bug_report.yml) |
| ask for a typed kind for an OpenBao API path | a [new kind request](https://github.com/alexandrust88/openbao-config-operator/issues/new?template=new_kind.yml) |
| report a vulnerability | privately, as described in [SECURITY.md](SECURITY.md), never in a public issue |
| report a conduct problem | privately, as described in [MAINTAINERS.md](MAINTAINERS.md#code-of-conduct-contact) |

Questions about OpenBao itself (server configuration, sealing, storage, plugins) belong to the
[OpenBao community](https://openbao.org/community/).

## Before you ask

- Check the resource's conditions: `kubectl describe <kind> <name>`. The `Ready` and `Synced` reasons and messages
  usually name the cause.
- Read the [troubleshooting guide](docs/operate/troubleshooting.md) and the [runbooks](docs/operate/runbooks.md).
- Search existing issues and discussions.

When you ask, include the operator version, the OpenBao version, the resource YAML and its status conditions.
Remove tokens, secret values and OpenBao response bodies first.

## Supported versions

Which operator releases get fixes is in the [release process](docs/maintainers/release-process.md#support-window);
which OpenBao and Kubernetes versions each release supports is in
[compatibility](docs/reference/compatibility.md).
