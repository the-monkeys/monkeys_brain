package discussions

import "time"

// CreateInput is a new site post when GroupSlug is empty, otherwise a group post.
type CreateInput struct {
	AccountID string
	GroupSlug string
	Body      string
	Audience  string
	Files     []File
}

// ListInput pages a feed. An empty GroupSlug is the public site feed.
// BeforePublicID is the last public id the client already has.
type ListInput struct {
	AccountID      string
	GroupSlug      string
	BeforePublicID string
}

// ReplyInput adds a reply. ParentPublicID empty means a top-level reply.
type ReplyInput struct {
	AccountID      string
	PostPublicID   string
	ParentPublicID string
	Body           string
}

// Post is a discussion row prepared for the API. Username is joined at read time.
type Post struct {
	PublicID       string
	Body           string
	Status         string
	AuthorUsername string
	AuthorGone     bool
	GroupSlug      string
	GroupName      string
	Audience       string
	ReplyCount     int32
	LikeCount      int32
	Liked          bool
	CreatedAt      time.Time
	EditedUntil    time.Time
	Files          []File
	Replies        []Reply
}

// Reply is top-level when ParentPublicID is empty, otherwise a reply to that reply at any depth.
type Reply struct {
	PublicID       string
	ParentPublicID string
	Body           string
	Status         string
	AuthorUsername string
	AuthorGone     bool
	CreatedAt      time.Time
}

// VisibleBody returns the text a viewer may see. Other people see an empty
// body once a post is deleted or hidden. The author and group staff still see it.
func VisibleBody(body, status string, authorOrStaff bool) string {
	if status == "visible" || authorOrStaff {
		return body
	}
	return ""
}
