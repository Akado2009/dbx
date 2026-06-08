#!/bin/bash
# fix ownership of mounted volume, then start server as postgres
chown -R postgres:postgres /var/lib/dbx/branches 2>/dev/null || true
exec gosu postgres dbx-server
