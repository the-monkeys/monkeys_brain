package models

import (
	"github.com/the-monkeys/the_monkeys/common/interservice"
)

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

type InterServiceMessage = interservice.Message

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
