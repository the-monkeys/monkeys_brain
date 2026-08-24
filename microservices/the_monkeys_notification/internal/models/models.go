package models

import (
	"database/sql"
	"time"
)

type Notification struct {
	ID                 int64     `json:"id"`
	NotificationTypeID int       `json:"notification_type_id"`
	NotificationName   string    `json:"notification_name"`
	Message            string    `json:"message"`
	RelatedBlogID      *int64    `json:"related_blog_id,omitempty"`
	RelatedUserID      *int64    `json:"related_user_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	Seen               bool      `json:"seen"`
	DeliveryStatus     string    `json:"delivery_status"`
	ChannelName        string    `json:"channel_name"`
}

type TheMonkeysMessage struct {
	Id           int64    `json:"id"`
	AccountId    string   `json:"account_id"`
	Username     string   `json:"username"`
	NewUsername  string   `json:"new_username"`
	FirstName    string   `json:"first_name"`
	LastName     string   `json:"last_name"`
	Email        string   `json:"email"`
	LoginMethod  string   `json:"login_method"`
	ClientId     string   `json:"client_id"`
	Client       string   `json:"client"`
	IpAddress    string   `json:"ip"`
	Action       string   `json:"action"`
	Notification string   `json:"notification"`
	BlogId       string   `json:"blog_id"`
	BlogIds      []string `json:"blog_ids,omitempty"`
	BlogStatus   string   `json:"blog_status"`
	BlogTitle    string   `json:"blog_title"`
	EventSlug    string   `json:"event_slug"`
	EventTitle   string   `json:"event_title"`
}

type TheMonkeysUser struct {
	Id                          int64          `json:"id"`
	AccountId                   string         `json:"account_id,omitempty"`
	Username                    string         `json:"username"`
	FirstName                   string         `json:"first_name"`
	LastName                    string         `json:"last_name"`
	Email                       string         `json:"email"`
	Password                    string         `json:"password"`
	PasswordVerificationToken   sql.NullString `json:"password_verification_token"`
	PasswordVerificationTimeout sql.NullTime   `json:"password_verification_timeout"`
	EmailVerificationStatus     string         `json:"email_verified"`
	UserStatus                  string         `json:"is_active,omitempty"`
	EmailVerificationToken      string         `json:"email_verification_token"`
	EmailVerificationTimeout    sql.NullTime   `json:"email_verification_timeout"`
	MobileVerificationToken     string         `json:"mobile_verification_token"`
	MobileVerificationTimeout   sql.NullTime   `json:"mobile_verification_timeout"`
	LoginMethod                 string         `json:"login_method"`
}
