package models

import "time"

// End of the struct

// type Blog struct {
// 	Time   int64   `json:"time"`
// 	Blocks []Block `json:"blocks"`
// }

// type Block struct {
// 	ID     string `json:"id"`
// 	Type   string `json:"type"`
// 	Data   Data   `json:"data"`
// 	Author string `json:"author"`
// 	Time   int64  `json:"time"`
// 	Tunes  Tunes  `json:"tunes,omitempty"`
// }

// type Data struct {
// 	Text string `json:"text"`
// }

// type Tunes struct {
// 	Footnotes []string `json:"footnotes"`
// }

type InterServiceMessage struct {
	AccountId           string    `json:"account_id"`
	UserStatus          string    `json:"user_status"`
	BlogId              string    `json:"blog_id"`
	BlogIds             []string  `json:"blog_ids,omitempty"`
	Action              string    `json:"action"`
	BlogStatus          string    `json:"blog_status"`
	IpAddress           string    `json:"ip_address"`
	Client              string    `json:"client"`
	Tags                []string  `json:"tags"`
	ScheduleTime        time.Time `json:"schedule_time"`
	Timezone            string    `json:"timezone"`
	AnalysisRequestedAt time.Time `json:"analysis_requested_at,omitempty"`
	CorrelationId       string    `json:"correlation_id,omitempty"`
	Priority            string    `json:"priority,omitempty"`
}

type DraftBlogRequest struct {
	BlogID         string   `json:"blog_id"`
	OwnerAccountID string   `json:"owner_account_id"`
	Blog           Blog     `json:"blog"`
	IsDraft        bool     `json:"is_draft"`
	IsArchive      bool     `json:"is_archive"`
	Tags           []string `json:"tags"`
}

type BlogResponse struct {
	Message string `json:"message"`
	Blog    Blog   `json:"blog"`
	Error   string `json:"error"`
}

type Blog struct {
	Time   int64   `json:"time"`
	Blocks []Block `json:"blocks"`
}

type Block struct {
	ID     string   `json:"id"`
	Type   string   `json:"type"`
	Data   Data     `json:"data"`
	Author []string `json:"author"`
	Time   int64    `json:"time"`
	Tunes  *Tunes   `json:"tunes,omitempty"`
}

type Data struct {
	Text           string   `json:"text,omitempty"`
	Level          int32    `json:"level,omitempty"`           // For headers
	File           *File    `json:"file,omitempty"`            // For files in attaches and images
	Items          []string `json:"items,omitempty"`           // For lists
	ListType       string   `json:"list_type,omitempty"`       // For list type (unordered, ordered)
	WithBorder     bool     `json:"with_border,omitempty"`     // For images
	WithBackground bool     `json:"with_background,omitempty"` // For images
	Stretched      bool     `json:"stretched,omitempty"`       // For images
	Caption        string   `json:"caption,omitempty"`         // For images
}

type File struct {
	URL       string `json:"url"`
	Size      int32  `json:"size,omitempty"`
	Name      string `json:"name,omitempty"`
	Extension string `json:"extension,omitempty"`
}

type Tunes struct {
	Footnotes []string `json:"footnotes"`
}
