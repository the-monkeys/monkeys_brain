package constants

// FRN Template names — registered once in FreeRangeNotify, referenced by name.
const (
	// Social
	FRNTplNewFollowerInApp = "new_follower_inapp"
	FRNTplNewFollowerSSE   = "new_follower_sse"
	FRNTplNewCommentInApp  = "new_comment_inapp"
	FRNTplNewCommentSSE    = "new_comment_sse"
	FRNTplBlogLikedInApp   = "blog_liked_inapp"
	FRNTplBlogLikedSSE     = "blog_liked_sse"

	// Collaboration
	FRNTplCoAuthorInviteInApp  = "coauthor_invite_inapp"
	FRNTplCoAuthorInviteSSE    = "coauthor_invite_sse"
	FRNTplCoAuthorInviteEmail  = "coauthor_invite_email"
	FRNTplCoAuthorAcceptInApp  = "coauthor_accept_inapp"
	FRNTplCoAuthorAcceptSSE    = "coauthor_accept_sse"
	FRNTplCoAuthorDeclineInApp = "coauthor_decline_inapp"
	FRNTplCoAuthorRemovedInApp = "coauthor_removed_inapp"
	FRNTplCoAuthorRemovedSSE   = "coauthor_removed_sse"

	// Content
	FRNTplBlogPublishedCoAuthorInApp = "blog_published_coauthor_inapp"
	FRNTplBlogPublishedCoAuthorSSE   = "blog_published_coauthor_sse"

	// Security
	FRNTplPasswordChangedInApp  = "password_changed_inapp"
	FRNTplPasswordChangedEmail  = "password_changed_email"
	FRNTplEmailVerifiedInApp    = "email_verified_inapp"
	FRNTplLoginDetectedInApp    = "login_detected_inapp"
	FRNTplLoginDetectedSSE      = "login_detected_sse"
	FRNTplLoginDetectedEmail    = "login_detected_email"
	FRNTplPasswordResetReqInApp = "password_reset_requested_inapp"
	FRNTplPasswordResetReqEmail = "password_reset_requested_email"
	FRNTplEmailChangedInApp     = "email_changed_inapp"
	FRNTplEmailChangedEmail     = "email_changed_email"
	FRNTplUsernameChangedInApp  = "username_changed_inapp"

	// Events
	FRNTplEventRSVPConfirmedInApp  = "event_rsvp_confirmed_inapp"
	FRNTplEventRSVPConfirmedEmail  = "event_rsvp_confirmed_email"
	FRNTplEventRSVPWaitlistedInApp = "event_rsvp_waitlisted_inapp"
	FRNTplEventWaitlistPromoInApp  = "event_waitlist_promoted_inapp"
	FRNTplEventWaitlistPromoEmail  = "event_waitlist_promoted_email"
	FRNTplEventReminderInApp       = "event_reminder_inapp"
	FRNTplEventReminderEmail       = "event_reminder_email"
	FRNTplEventCancelledInApp      = "event_cancelled_inapp"
	FRNTplEventCancelledEmail      = "event_cancelled_email"
	FRNTplEventNewByFollowedInApp  = "event_new_by_followed_inapp"
	FRNTplEventCommentInApp        = "event_comment_inapp"
	FRNTplEventRefundInApp         = "event_refund_inapp"
	FRNTplEventRefundEmail         = "event_refund_email"

	FRNTplEventApplicationReceivedInApp = "event_application_received_inapp"
	FRNTplEventApplicationReceivedSSE   = "event_application_received_sse"
	FRNTplEventApplicationReceivedEmail = "event_application_received_email"
	FRNTplEventApplicationApprovedInApp = "event_application_approved_inapp"
	FRNTplEventApplicationApprovedSSE   = "event_application_approved_sse"
	FRNTplEventApplicationApprovedEmail = "event_application_approved_email"
	FRNTplEventApplicationRejectedInApp = "event_application_rejected_inapp"
	FRNTplEventApplicationRejectedSSE   = "event_application_rejected_sse"
	FRNTplEventApplicationRejectedEmail = "event_application_rejected_email"
	FRNTplEventRSVPHostNoticeInApp      = "event_rsvp_host_notice_inapp"
	FRNTplEventRSVPHostNoticeSSE        = "event_rsvp_host_notice_sse"
	FRNTplEventUpdatedInApp             = "event_updated_inapp"
	FRNTplEventUpdatedSSE               = "event_updated_sse"
	FRNTplEventUpdatedEmail             = "event_updated_email"

	FRNTplGroupJoinRequestedInApp = "group_join_requested_inapp"
	FRNTplGroupJoinRequestedSSE   = "group_join_requested_sse"
	FRNTplGroupJoinRequestedEmail = "group_join_requested_email"
	FRNTplGroupJoinApprovedInApp  = "group_join_approved_inapp"
	FRNTplGroupJoinApprovedSSE    = "group_join_approved_sse"
	FRNTplGroupJoinApprovedEmail  = "group_join_approved_email"
	FRNTplGroupJoinRejectedInApp  = "group_join_rejected_inapp"
	FRNTplGroupJoinRejectedSSE    = "group_join_rejected_sse"
	FRNTplGroupJoinRejectedEmail  = "group_join_rejected_email"
	FRNTplGroupMemberJoinedInApp  = "group_member_joined_inapp"
	FRNTplGroupMemberJoinedSSE    = "group_member_joined_sse"
	FRNTplGroupEventPublishedInApp = "group_event_published_inapp"
	FRNTplGroupEventPublishedSSE   = "group_event_published_sse"

	// Account lifecycle
	FRNTplAccountDeletedInApp     = "account_deleted_inapp"
	FRNTplAccountDeletedEmail     = "account_deleted_email"
	FRNTplAccountDeactivatedInApp = "account_deactivated_inapp"
	FRNTplAccountReactivatedInApp = "account_reactivated_inapp"
)

// FRN Categories
const (
	FRNCategorySocial        = "social"
	FRNCategoryCollaboration = "collaboration"
	FRNCategoryContent       = "content"
	FRNCategorySecurity      = "security"
	FRNCategoryAccount       = "account"
	FRNCategoryEvents        = "events"
)
