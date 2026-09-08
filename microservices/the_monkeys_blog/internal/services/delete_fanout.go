package services

import "net/http"

// esDeleteOK reports whether an Elasticsearch document delete should be treated
// as success. 404 is success so a missing ES doc still fans out BLOG_DELETE
// to Postgres and storage (orphans / already-deleted ES).
func esDeleteOK(statusCode int) bool {
	return statusCode == http.StatusOK || statusCode == http.StatusNotFound
}

// blogDeleteFanOutKeys returns the users-service and storage-service routing
// keys used by a single-blog hard delete. Index contract matches
// RABBITMQ_ROUTING_KEYS: [1] users (key2), [2] storage (blog_svc_file_svc_key).
func blogDeleteFanOutKeys(keys []string) (users, storage string, ok bool) {
	if len(keys) < 3 {
		return "", "", false
	}
	return keys[1], keys[2], true
}
