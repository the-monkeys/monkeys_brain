package db

import (
	"context"
	"time"
)

// logConnectionIdentity records non-secret connection diagnostics at startup.
// It only reads connection/catalog information, never account data or credentials.
// Failure is nonfatal and cannot change authentication or authorization behavior.
func (adh *authDBHandler) logConnectionIdentity() {
	if adh == nil || adh.db == nil || adh.log == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var name, user, schema, searchPath, server, accountTable, roleTable string
	var port int
	err := adh.db.QueryRowContext(ctx, `SELECT current_database(), current_user,
        COALESCE(current_schema(), ''), current_setting('search_path'),
        COALESCE(inet_server_addr()::text, ''), COALESCE(inet_server_port(), 0),
        COALESCE((SELECT n.nspname || '.' || c.relname FROM pg_class c
                  JOIN pg_namespace n ON n.oid = c.relnamespace
                  WHERE c.oid = to_regclass('user_account')), 'missing'),
        COALESCE((SELECT n.nspname || '.' || c.relname FROM pg_class c
                  JOIN pg_namespace n ON n.oid = c.relnamespace
                  WHERE c.oid = to_regclass('user_role')), 'missing')`).
		Scan(&name, &user, &schema, &searchPath, &server, &port, &accountTable, &roleTable)
	if err != nil {
		// Driver errors can contain connection details. Never log the raw error/DSN.
		adh.log.Warnw("authz database identity unavailable", "reason", "database_identity_unavailable")
		return
	}
	adh.log.Infow("authz database identity", "db_name", name, "db_user", user,
		"db_schema", schema, "db_search_path", searchPath, "db_server", server,
		"db_port", port, "user_account_table", accountTable, "user_role_table", roleTable)
}
