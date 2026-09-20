package blogacl

func FilterReadableBlogs(blogs []map[string]interface{}, viewer string, canView func(doc map[string]interface{}, viewer string) bool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(blogs))
	for _, blog := range blogs {
		if canView != nil && canView(blog, viewer) {
			out = append(out, blog)
		}
	}
	return out
}
