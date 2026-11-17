"""Operator-managed postgresql credentials, tested over verified TLS."""
from . import sql_common

NAME = 'postgresql'
NAMESPACE = 'obco-db-' + NAME


def install():
    sql_common.install(NAME)


def connection(tenant):
    return sql_common.connection(NAME, tenant)


def role(tenant):
    return sql_common.role(NAME, tenant)


def probe(tenant, credential_secret_name, mode):
    sql_common.probe(NAME, tenant, credential_secret_name, mode)
