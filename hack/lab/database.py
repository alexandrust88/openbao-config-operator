#!/usr/bin/env python3
"""Configure operator-managed dynamic database credentials and test their real database privileges."""
import argparse
import importlib

from databases import common

PROVIDERS = ('postgresql', 'mysql', 'mariadb', 'cassandra', 'influxdb', 'valkey')

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('up', 'test'))
    parser.add_argument('provider', choices=PROVIDERS)
    args = parser.parse_args()
    try:
        provider = importlib.import_module('databases.' + args.provider)
        {'up': common.up, 'test': common.test}[args.command](provider)
        if args.command == 'test' and args.provider == 'postgresql':
            from databases import postgresql_lifecycle
            postgresql_lifecycle.test()
    except (RuntimeError, KeyError, ValueError) as error:
        raise SystemExit(str(error) if isinstance(error, RuntimeError) else 'Database fixture returned an invalid response') from None
