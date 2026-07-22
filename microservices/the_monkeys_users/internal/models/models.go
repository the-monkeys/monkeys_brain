package models

import (
	"database/sql"
)

// TODO: Change as per schema user_account table
type UserAccount struct {
	Id            int64          `json:"id"`
	AccountId     string         `json:"account_id"`
	UserName      string         `json:"username"`
	FirstName     string         `json:"firstname"`
	LastName      string         `json:"lastname"`
	Email         string         `json:"email"`
	DateOfBirth   sql.NullTime   `json:"date_of_birth"`
	Bio           sql.NullString `json:"bio"`
	AvatarUrl     sql.NullString `json:"avatar_url"`
	Address       sql.NullString `json:"address"`
	ContactNumber sql.NullInt64  `json:"contact_number"`
	UserStatus    string         `json:"user_status"`
	CreatedAt     sql.NullTime   `json:"created_at,omitempty"`
	LinkedIn      sql.NullString `json:"linkedIn"`
	Github        sql.NullString `json:"github"`
	Twitter       sql.NullString `json:"twitter"`
	Instagram     sql.NullString `json:"instagram"`
	Interests     []string       `json:"interests"`
}

type UserAuthInfo struct {
	Id                       int64          `json:"id"`
	UserId                   int64          `json:"user_id"`
	PasswordHash             sql.NullString `json:"password_hash"`
	CreatedAt                sql.NullTime   `json:"created_at"`
	PasswordRecoveryToken    sql.NullString `json:"password_recovery_token"`
	PasswordRecoveryTimeout  sql.NullTime   `json:"password_recovery_timeout"`
	PasswordUpdatedAt        sql.NullTime   `json:"password_updated_at"`
	EmailVerificationToken   string         `json:"email_verification_token"`
	EmailVerificationTimeout sql.NullTime   `json:"email_verification_timeout"`
	EmailValidationStatus    string         `json:"email_validation_satus"`
	EmailValidationTime      sql.NullTime   `json:"email_validation_time"`
	AuthProviderId           int64          `json:"auth_provider_id"`
}

type TheMonkeysUser struct {
	Id                          int64          `json:"id"`
	AccountId                   string         `json:"account_id,omitempty"`
	Username                    string         `json:"username"`
	FirstName                   string         `json:"first_name"`
	LastName                    string         `json:"last_name"`
	Bio                         sql.NullString `json:"bio"`
	Location                    sql.NullString `json:"location"`
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

type UserProfileRes struct {
	AccountId      string         `json:"account_id,omitempty"`
	Username       string         `json:"username,omitempty"`
	FirstName      string         `json:"first_name,omitempty"`
	LastName       string         `json:"last_name,omitempty"`
	Email          string         `json:"email,omitempty"`
	DateOfBirth    sql.NullTime   `json:"date_of_birth,omitempty"`
	RoleId         int64          `json:"role_id,omitempty"`
	Bio            sql.NullString `json:"bio,omitempty"`
	AvatarUrl      sql.NullString `json:"avatar_url,omitempty"`
	CreatedAt      sql.NullTime   `json:"created_at,omitempty"`
	UpdatedAt      sql.NullTime   `json:"updated_at,omitempty"`
	Address        sql.NullString `json:"address,omitempty"`
	ContactNumber  sql.NullString `json:"contact_number,omitempty"`
	UserStatus     string         `json:"user_status,omitempty"`
	ViewPermission string         `json:"view_permission,omitempty"`
	LinkedIn       sql.NullString `json:"linkedIn"`
	Github         sql.NullString `json:"github"`
	Twitter        sql.NullString `json:"twitter"`
	Instagram      sql.NullString `json:"instagram"`
	Interests      []string       `json:"interests"`
}

type UserLogs struct {
	AccountId string `json:"account_id,omitempty"`
	Client    string `json:"client,omitempty"`
	IpAddress string `json:"ip_address,omitempty"`
	OS        string `json:"os,omitempty"`
	Country   string `json:"country,omitempty"`
}

// BusinessCard mirrors a row of the business_cards table. CardState holds the
// full editor document as raw JSON (single source of truth); the discrete
// columns are queryable metadata only.
type BusinessCard struct {
	Id                  string         `json:"id"`
	AccountId           string         `json:"account_id"`
	Name                string         `json:"name"`
	TemplateId          string         `json:"template_id"`
	ThemeId             string         `json:"theme_id"`
	CardState           string         `json:"card_state"`
	IsDefault           bool           `json:"is_default"`
	AvatarAssetChecksum sql.NullString `json:"avatar_asset_checksum"`
	LogoAssetChecksum   sql.NullString `json:"logo_asset_checksum"`
	CreatedAt           sql.NullTime   `json:"created_at"`
	UpdatedAt           sql.NullTime   `json:"updated_at"`
}

// UserAddress is a PRIVATE, owner-scoped postal address. Never exposed on the
// public profile. Reusable across business cards and (future) billing/shipping.
type UserAddress struct {
	Id         string         `json:"id"`
	AccountId  string         `json:"account_id"`
	Label      string         `json:"label"`
	Line1      string         `json:"line1"`
	Line2      sql.NullString `json:"line2"`
	City       sql.NullString `json:"city"`
	State      sql.NullString `json:"state"`
	PostalCode sql.NullString `json:"postal_code"`
	Country    sql.NullString `json:"country"`
	IsDefault  bool           `json:"is_default"`
	CreatedAt  sql.NullTime   `json:"created_at"`
	UpdatedAt  sql.NullTime   `json:"updated_at"`
}

type TheMonkeysMessage struct {
	Id           int64    `json:"id"`
	AccountId    string   `json:"account_id"`
	Username     string   `json:"username"`
	NewUsername  string   `json:"new_username"`
	Email        string   `json:"email"`
	ClientId     string   `json:"client_id"`
	Client       string   `json:"client"`
	IpAddress    string   `json:"ip_address"`
	Action       string   `json:"action"`
	BlogId       string   `json:"blog_id"`
	BlogIds      []string `json:"blog_ids,omitempty"`
	BlogStatus   string   `json:"blog_status"`
	Notification string   `json:"notification"`
	Tags         []string `json:"tags"`
	BlogTitle    string   `json:"blog_title"`
}

type Blog struct {
	Id         int64  `json:"id"`
	BlogId     string `json:"blog_id"`
	Username   string `json:"username"`
	AccountId  string `json:"account_id"`
	Permission string `json:"permission"`
	BlogStatus string `json:"blog_status"`
}
