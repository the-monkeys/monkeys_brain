package interservice

import (
	"encoding/json"
	"time"
)

// Message is the single RabbitMQ JSON envelope for inter-service communication.
// Producers and consumers must use this type (or a type alias of it).
//
// JSON aliases exist because production already disagrees:
// users/blog emit ip_address + blog_status; storage/authz often read ip / status.
// Marshal writes both keys; unmarshal accepts either. If both are present and
// differ, the canonical key wins.
type Message struct {
	Id                  int64     `json:"id,omitempty"`
	AccountId           string    `json:"account_id,omitempty"`
	Username            string    `json:"username,omitempty"`
	NewUsername         string    `json:"new_username,omitempty"`
	FirstName           string    `json:"first_name,omitempty"`
	LastName            string    `json:"last_name,omitempty"`
	Email               string    `json:"email,omitempty"`
	LoginMethod         string    `json:"login_method,omitempty"`
	ClientId            string    `json:"client_id,omitempty"`
	Client              string    `json:"client,omitempty"`
	IpAddress           string    `json:"ip_address,omitempty"`
	Action              string    `json:"action,omitempty"`
	Notification        string    `json:"notification,omitempty"`
	UserStatus          string    `json:"user_status,omitempty"`
	BlogId              string    `json:"blog_id,omitempty"`
	BlogIds             []string  `json:"blog_ids,omitempty"`
	BlogStatus          string    `json:"blog_status,omitempty"`
	BlogTitle           string    `json:"blog_title,omitempty"`
	Tags                []string  `json:"tags,omitempty"`
	ScheduleTime        time.Time `json:"schedule_time,omitempty"`
	Timezone            string    `json:"timezone,omitempty"`
	AnalysisRequestedAt time.Time `json:"analysis_requested_at,omitempty"`
	CorrelationId       string    `json:"correlation_id,omitempty"`
	Priority            string    `json:"priority,omitempty"`
	EventSlug           string    `json:"event_slug,omitempty"`
	EventTitle          string    `json:"event_title,omitempty"`
	EventIds            []string  `json:"event_ids,omitempty"`
	GroupIds            []string  `json:"group_ids,omitempty"`
	GroupSlug           string    `json:"group_slug,omitempty"`
}

type messageDTO struct {
	Id                  int64      `json:"id,omitempty"`
	AccountId           string     `json:"account_id,omitempty"`
	Username            string     `json:"username,omitempty"`
	NewUsername         string     `json:"new_username,omitempty"`
	FirstName           string     `json:"first_name,omitempty"`
	LastName            string     `json:"last_name,omitempty"`
	Email               string     `json:"email,omitempty"`
	LoginMethod         string     `json:"login_method,omitempty"`
	ClientId            string     `json:"client_id,omitempty"`
	Client              string     `json:"client,omitempty"`
	IpAddress           string     `json:"ip_address,omitempty"`
	Ip                  string     `json:"ip,omitempty"`
	Action              string     `json:"action,omitempty"`
	Notification        string     `json:"notification,omitempty"`
	UserStatus          string     `json:"user_status,omitempty"`
	BlogId              string     `json:"blog_id,omitempty"`
	BlogIds             []string   `json:"blog_ids,omitempty"`
	BlogStatus          string     `json:"blog_status,omitempty"`
	Status              string     `json:"status,omitempty"`
	BlogTitle           string     `json:"blog_title,omitempty"`
	Tags                []string   `json:"tags,omitempty"`
	ScheduleTime        *time.Time `json:"schedule_time,omitempty"`
	Timezone            string     `json:"timezone,omitempty"`
	AnalysisRequestedAt *time.Time `json:"analysis_requested_at,omitempty"`
	CorrelationId       string     `json:"correlation_id,omitempty"`
	Priority            string     `json:"priority,omitempty"`
	EventSlug           string     `json:"event_slug,omitempty"`
	EventTitle          string     `json:"event_title,omitempty"`
	EventIds            []string   `json:"event_ids,omitempty"`
	GroupIds            []string   `json:"group_ids,omitempty"`
	GroupSlug           string     `json:"group_slug,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	d := messageDTO{
		Id:            m.Id,
		AccountId:     m.AccountId,
		Username:      m.Username,
		NewUsername:   m.NewUsername,
		FirstName:     m.FirstName,
		LastName:      m.LastName,
		Email:         m.Email,
		LoginMethod:   m.LoginMethod,
		ClientId:      m.ClientId,
		Client:        m.Client,
		IpAddress:     m.IpAddress,
		Ip:            m.IpAddress,
		Action:        m.Action,
		Notification:  m.Notification,
		UserStatus:    m.UserStatus,
		BlogId:        m.BlogId,
		BlogIds:       m.BlogIds,
		BlogStatus:    m.BlogStatus,
		Status:        m.BlogStatus,
		BlogTitle:     m.BlogTitle,
		Tags:          m.Tags,
		Timezone:      m.Timezone,
		CorrelationId: m.CorrelationId,
		Priority:      m.Priority,
		EventSlug:     m.EventSlug,
		EventTitle:    m.EventTitle,
		EventIds:      m.EventIds,
		GroupIds:      m.GroupIds,
		GroupSlug:     m.GroupSlug,
	}
	if !m.ScheduleTime.IsZero() {
		t := m.ScheduleTime
		d.ScheduleTime = &t
	}
	if !m.AnalysisRequestedAt.IsZero() {
		t := m.AnalysisRequestedAt
		d.AnalysisRequestedAt = &t
	}
	return json.Marshal(d)
}

func (m *Message) UnmarshalJSON(data []byte) error {
	var d messageDTO
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	m.Id = d.Id
	m.AccountId = d.AccountId
	m.Username = d.Username
	m.NewUsername = d.NewUsername
	m.FirstName = d.FirstName
	m.LastName = d.LastName
	m.Email = d.Email
	m.LoginMethod = d.LoginMethod
	m.ClientId = d.ClientId
	m.Client = d.Client
	m.IpAddress = d.IpAddress
	if m.IpAddress == "" {
		m.IpAddress = d.Ip
	}
	m.Action = d.Action
	m.Notification = d.Notification
	m.UserStatus = d.UserStatus
	m.BlogId = d.BlogId
	m.BlogIds = d.BlogIds
	m.BlogStatus = d.BlogStatus
	if m.BlogStatus == "" {
		m.BlogStatus = d.Status
	}
	m.BlogTitle = d.BlogTitle
	m.Tags = d.Tags
	if d.ScheduleTime != nil {
		m.ScheduleTime = *d.ScheduleTime
	}
	m.Timezone = d.Timezone
	if d.AnalysisRequestedAt != nil {
		m.AnalysisRequestedAt = *d.AnalysisRequestedAt
	}
	m.CorrelationId = d.CorrelationId
	m.Priority = d.Priority
	m.EventSlug = d.EventSlug
	m.EventTitle = d.EventTitle
	m.EventIds = d.EventIds
	m.GroupIds = d.GroupIds
	m.GroupSlug = d.GroupSlug
	return nil
}
