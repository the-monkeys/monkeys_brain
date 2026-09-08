DROP TABLE IF EXISTS group_admin_flags;
DROP TABLE IF EXISTS event_admin_flags;
DROP TABLE IF EXISTS blog_admin_flags;
DROP TABLE IF EXISTS user_admin_flags;
DROP TABLE IF EXISTS admin_audit_log;

DELETE FROM user_status us
 WHERE us.status = 'suspended'
   AND NOT EXISTS (SELECT 1 FROM user_account ua WHERE ua.user_status = us.id);

DELETE FROM user_role ur
 WHERE ur.role_desc = 'Community'
   AND NOT EXISTS (SELECT 1 FROM user_account ua WHERE ua.role_id = ur.id);
