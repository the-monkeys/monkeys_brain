package pb

// GetScheduledAtOverride returns ScheduledAt as the rendition scheduled time override.
func (x *Rendition) GetScheduledAtOverride() string {
	if x != nil {
		return x.ScheduledAt
	}
	return ""
}

// GetScheduleTimezoneOverride returns ScheduleTimezone as the rendition schedule timezone override.
func (x *Rendition) GetScheduleTimezoneOverride() string {
	if x != nil {
		return x.ScheduleTimezone
	}
	return ""
}
