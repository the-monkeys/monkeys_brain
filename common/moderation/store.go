package moderation

import (
	"context"
	"database/sql"
	"encoding/json"
	"unicode/utf8"

	"errors"
	"strings"
	"time"

	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"github.com/the-monkeys/the_monkeys/common/audience"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"github.com/the-monkeys/the_monkeys/constants"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Store persists ratings and decisions separately from content and its ACL.
// It has no authorization cache: identities, DOBs and roles come from this DB.
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

type storeQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
type storeActor struct {
	id                      int64
	account, username, role string
	birth                   sql.NullTime
}

func (a storeActor) staff() bool { return staffaccess.Has(a.role, staffaccess.ModerationNSFW) }
func loadStoreActor(ctx context.Context, q storeQuery, account string) (storeActor, error) {
	a := storeActor{account: account}
	if account == "" {
		return a, nil
	}
	err := q.QueryRowContext(ctx, `SELECT u.id,u.username,COALESCE(r.role_desc,''),u.date_of_birth FROM user_account u LEFT JOIN user_role r ON r.id=u.role_id JOIN user_status s ON s.id=u.user_status WHERE u.account_id=$1 AND lower(s.status)='active'`, account).Scan(&a.id, &a.username, &a.role, &a.birth)
	if errors.Is(err, sql.ErrNoRows) {
		return storeActor{}, nil
	}
	return a, err
}
func storeTarget(t *pb.Target) error {
	if t == nil || t.Id == "" || strings.TrimSpace(t.Id) != t.Id || len(t.Id) > 255 {
		return status.Error(codes.InvalidArgument, "invalid moderation target")
	}
	switch t.Kind {
	case "blog", "event", "group", "discussion":
		return nil
	}
	return status.Error(codes.InvalidArgument, "invalid moderation target kind")
}
func storeFailure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	if status.Code(err) != codes.Unknown {
		return err
	}
	return status.Error(codes.Internal, "moderation persistence failed")
}
func (s *Store) transaction(ctx context.Context, read bool) (*sql.Tx, error) {
	opts := &sql.TxOptions{}
	if read {
		opts.ReadOnly = true
		opts.Isolation = sql.LevelRepeatableRead
	}
	return s.db.BeginTx(ctx, opts)
}

// groupMemberActive deliberately requires an active membership, including for
// private/unlisted group audiences. A pending or banned row never grants access.
func groupMemberActive(ctx context.Context, q storeQuery, groupID, userID int64) (bool, error) {
	if groupID == 0 || userID == 0 {
		return false, nil
	}
	var active bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id=$1 AND user_id=$2 AND status='active')`, groupID, userID).Scan(&active)
	return active, err
}

type storeContent struct {
	id, owner, group                                                         int64
	ownerAccount, status, visibility, groupVisibility, groupStatus, audience string
	host, attending                                                          bool
}

func loadStoreContent(ctx context.Context, q storeQuery, a storeActor, t *pb.Target) (storeContent, bool, error) {
	var c storeContent
	var err error
	// These projections intentionally contain no title, body, media or other raw content.
	switch t.Kind {
	case "blog":
		err = q.QueryRowContext(ctx, `SELECT b.id,b.user_id,u.account_id,COALESCE(b.status,''),COALESCE(b.group_id,0),b.audience,COALESCE(g.visibility,''),COALESCE(g.status,'') FROM blog b JOIN user_account u ON u.id=b.user_id LEFT JOIN groups g ON g.id=b.group_id WHERE b.blog_id=$1`, t.Id).Scan(&c.id, &c.owner, &c.ownerAccount, &c.status, &c.group, &c.audience, &c.groupVisibility, &c.groupStatus)
	case "event":
		err = q.QueryRowContext(ctx, `SELECT e.id,e.organizer_id,u.account_id,COALESCE(e.status,''),COALESCE(e.group_id,0),COALESCE(e.visibility,'public'),COALESCE(g.visibility,''),COALESCE(g.status,''),EXISTS(SELECT 1 FROM event_co_hosts h WHERE h.event_id=e.id AND h.co_host_id=$2),EXISTS(SELECT 1 FROM event_attendees r WHERE r.event_id=e.id AND r.user_id=$2 AND r.status='confirmed') FROM events e JOIN user_account u ON u.id=e.organizer_id LEFT JOIN groups g ON g.id=e.group_id WHERE e.slug=$1`, t.Id, a.id).Scan(&c.id, &c.owner, &c.ownerAccount, &c.status, &c.group, &c.visibility, &c.groupVisibility, &c.groupStatus, &c.host, &c.attending)
	case "group":
		err = q.QueryRowContext(ctx, `SELECT g.id,g.organizer_id,u.account_id,g.status,g.visibility FROM groups g JOIN user_account u ON u.id=g.organizer_id WHERE g.slug=$1`, t.Id).Scan(&c.id, &c.owner, &c.ownerAccount, &c.status, &c.visibility)
		c.group = c.id
	case "discussion":
		err = q.QueryRowContext(ctx, `SELECT p.id,COALESCE(p.author_id,0),COALESCE(u.account_id,''),p.status,COALESCE(p.group_id,0),p.audience,COALESCE(g.visibility,''),COALESCE(g.status,'') FROM discussion_posts p LEFT JOIN user_account u ON u.id=p.author_id LEFT JOIN groups g ON g.id=p.group_id WHERE p.public_id=$1`, t.Id).Scan(&c.id, &c.owner, &c.ownerAccount, &c.status, &c.group, &c.audience, &c.groupVisibility, &c.groupStatus)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	own := a.id != 0 && a.id == c.owner
	member, err := groupMemberActive(ctx, q, c.group, a.id)
	if err != nil {
		return c, false, err
	}
	switch t.Kind {
	case "blog":
		if own {
			return c, true, nil
		}
		if c.status != constants.BlogStatusPublished {
			return c, false, nil
		}
		return c, audience.CanRead(audience.Coerce(c.audience, c.groupVisibility), c.ownerAccount, a.account, member), nil
	case "event":
		if own || c.host {
			return c, true, nil
		}
		if c.status == "draft" || c.status == "deleted" || c.status == "suspended" {
			return c, false, nil
		}
		visible := c.visibility == "public" || c.visibility == "unlisted" || (c.visibility == "group_members" && member) || c.attending
		return c, visible && (c.group == 0 || c.groupVisibility == "public" || member || c.attending), nil
	case "group":
		return c, own || (c.status == "published" && (c.visibility == "public" || member)), nil
	case "discussion":
		if c.group != 0 && c.groupStatus != "published" {
			return c, false, nil
		}
		if c.status != "visible" && !own {
			return c, false, nil
		}
		return c, audience.CanRead(audience.Coerce(c.audience, c.groupVisibility), c.ownerAccount, a.account, member), nil
	}
	return c, false, nil
}
func lockStoreRating(ctx context.Context, tx *sql.Tx, t *pb.Target) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO content_moderation(target_kind,target_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, t.Kind, t.Id); err != nil {
		return err
	}
	var revision int64
	return tx.QueryRowContext(ctx, `SELECT revision FROM content_moderation WHERE target_kind=$1 AND target_id=$2 FOR UPDATE`, t.Kind, t.Id).Scan(&revision)
}
func storeLegacyNSFW(ctx context.Context, q storeQuery, c storeContent, kind string) (bool, error) {
	var query string
	switch kind {
	case "blog":
		query = `SELECT EXISTS(SELECT 1 FROM blog_admin_flags WHERE blog_id=$1 AND flag_type='nsfw')`
	case "event":
		query = `SELECT EXISTS(SELECT 1 FROM event_admin_flags WHERE event_id=$1 AND flag_type='nsfw')`
	case "group":
		query = `SELECT EXISTS(SELECT 1 FROM group_admin_flags WHERE group_id=$1 AND flag_type='nsfw')`
	default:
		return false, nil
	}
	var flagged bool
	err := q.QueryRowContext(ctx, query, c.id).Scan(&flagged)
	return flagged, err
}
func storeAccess(ctx context.Context, q storeQuery, a storeActor, t *pb.Target, c storeContent, visible bool) (*pb.Access, error) {
	return storeAccessWithScanner(ctx, q, a, t, c, visible, nil)
}

// A nil scannerFlag preserves scalar reads for media and mutation callers.
func storeAccessWithScanner(ctx context.Context, q storeQuery, a storeActor, t *pb.Target, c storeContent, visible bool, scannerFlag *bool) (*pb.Access, error) {
	out := &pb.Access{Target: &pb.Target{Kind: t.Kind, Id: t.Id}}
	if !visible {
		out.Reason = "not_found"
		return out, nil
	}
	out.Revision = 1
	out.ScanStatus = "unknown"
	var author, staff, scanner bool
	err := q.QueryRowContext(ctx, `SELECT author_nsfw,staff_nsfw,scanner_nsfw,revision,scan_status FROM content_moderation WHERE target_kind=$1 AND target_id=$2`, t.Kind, t.Id).Scan(&author, &staff, &scanner, &out.Revision, &out.ScanStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	legacy, err := storeLegacyNSFW(ctx, q, c, t.Kind)
	if err != nil {
		return nil, err
	}
	var media bool
	if scannerFlag == nil {
		media, err = storeTargetScanner(ctx, q, t)
		if err != nil {
			return nil, err
		}
	} else {
		media = *scannerFlag
	}
	out.IsNsfw = author || staff || scanner || legacy || media
	if !out.IsNsfw {
		return out, nil
	}
	why := Eligibility(a.id != 0, a.birth, time.Now())
	if why != "eligible" {
		out.Reason = why
		return out, nil
	}
	out.CanReveal = true
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM content_reveals WHERE target_kind=$1 AND target_id=$2 AND account_id=$3 AND revision=$4)`, t.Kind, t.Id, a.account, out.Revision).Scan(&out.Revealed)
	if err != nil {
		return nil, err
	}
	if !out.Revealed {
		out.Reason = "reveal_required"
	}
	return out, nil
}

func (s *Store) Check(ctx context.Context, req *pb.CheckRequest) (*pb.CheckResponse, error) {
	if req == nil || len(req.Targets) > 100 {
		return nil, status.Error(codes.InvalidArgument, "invalid moderation check")
	}
	tx, err := s.transaction(ctx, true)
	if err != nil {
		return nil, storeFailure(err)
	}
	defer tx.Rollback()
	a, err := loadStoreActor(ctx, tx, req.AccountId)
	if err != nil {
		return nil, storeFailure(err)
	}
	contents := make([]storeContent, len(req.Targets))
	visible := make([]bool, len(req.Targets))
	scannerTargets := make([]*pb.Target, 0, len(req.Targets))
	for i, t := range req.Targets {
		if err = storeTarget(t); err != nil {
			return nil, err
		}
		contents[i], visible[i], err = loadStoreContent(ctx, tx, a, t)
		if err != nil {
			return nil, storeFailure(err)
		}
		if visible[i] {
			scannerTargets = append(scannerTargets, t)
		}
	}
	scannerFlags, err := storeTargetScanners(ctx, tx, scannerTargets)
	if err != nil {
		return nil, storeFailure(err)
	}
	out := &pb.CheckResponse{Items: make([]*pb.Access, 0, len(req.Targets))}
	for i, t := range req.Targets {
		flagged := scannerFlags[storeScannerKey{kind: t.Kind, id: t.Id}]
		access, e := storeAccessWithScanner(ctx, tx, a, t, contents[i], visible[i], &flagged)
		if e != nil {
			return nil, storeFailure(e)
		}
		out.Items = append(out.Items, access)
	}
	if err = tx.Commit(); err != nil {
		return nil, storeFailure(err)
	}
	return out, nil
}

type storeMutation func(*sql.Tx, storeActor, storeContent) (*pb.Report, error)

func (s *Store) mutate(ctx context.Context, req *pb.ActionRequest, fn storeMutation) (*pb.ActionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "action required")
	}
	if err := storeTarget(req.Target); err != nil {
		return nil, err
	}
	tx, err := s.transaction(ctx, false)
	if err != nil {
		return nil, storeFailure(err)
	}
	defer tx.Rollback()
	a, err := loadStoreActor(ctx, tx, req.GetActor().GetAccountId())
	if err != nil {
		return nil, storeFailure(err)
	}
	if a.id == 0 {
		return nil, status.Error(codes.Unauthenticated, "sign in required")
	}
	if err = lockStoreRating(ctx, tx, req.Target); err != nil {
		return nil, storeFailure(err)
	}
	c, visible, err := loadStoreContent(ctx, tx, a, req.Target)
	if err != nil {
		return nil, storeFailure(err)
	}
	if !visible {
		return nil, status.Error(codes.NotFound, "content not found")
	}
	report, err := fn(tx, a, c)
	if err != nil {
		return nil, storeFailure(err)
	}
	access, err := storeAccess(ctx, tx, a, req.Target, c, true)
	if err != nil {
		return nil, storeFailure(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, storeFailure(err)
	}
	return &pb.ActionResponse{Report: report, Access: access}, nil
}
func auditStore(ctx context.Context, tx *sql.Tx, a storeActor, ip, action string, t *pb.Target, payload map[string]any) error {
	payload["actor_account_id"] = a.account
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO admin_audit_log(actor_ip,action,entity_type,entity_id,payload) VALUES($1,$2,$3,$4,$5::jsonb)`, ip, action, t.Kind, t.Id, string(raw))
	return err
}

const storeReportProjection = `r.id,r.target_kind,r.target_id,r.reason,r.details,r.status,COALESCE(u.username,r.reporter_username),r.created_at,r.reviewed_by,r.reviewed_at,r.review_note`
const storeReportFrom = ` FROM content_reports r LEFT JOIN user_account u ON u.account_id=r.reporter_account_id `

type storeScanner interface{ Scan(...any) error }

func scanStoreReport(row storeScanner) (*pb.Report, error) {
	r := &pb.Report{Target: &pb.Target{}}
	var created time.Time
	var reviewed sql.NullTime
	err := row.Scan(&r.Id, &r.Target.Kind, &r.Target.Id, &r.Reason, &r.Details, &r.Status, &r.ReporterUsername, &created, &r.ReviewedBy, &reviewed, &r.ReviewNote)
	if r.Status == "confirmed_nsfw" {
		r.Status = "action_taken"
	}
	r.CreatedAt = created.UTC().Format(time.RFC3339Nano)
	if reviewed.Valid {
		r.ReviewedAt = reviewed.Time.UTC().Format(time.RFC3339Nano)
	}
	return r, err
}
func readStoreReport(ctx context.Context, q storeQuery, id int64) (*pb.Report, error) {
	return scanStoreReport(q.QueryRowContext(ctx, `SELECT `+storeReportProjection+storeReportFrom+` WHERE r.id=$1`, id))
}
func (s *Store) ReportContent(ctx context.Context, req *pb.ActionRequest) (*pb.ActionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "report required")
	}
	reason := strings.ToLower(strings.TrimSpace(req.Reason))
	if reason == "" || utf8.RuneCountInString(reason) > 100 || utf8.RuneCountInString(req.Details) > 10000 {
		return nil, status.Error(codes.InvalidArgument, "invalid report reason or details")
	}
	return s.mutate(ctx, req, func(tx *sql.Tx, a storeActor, c storeContent) (*pb.Report, error) {
		var id int64
		err := tx.QueryRowContext(ctx, `INSERT INTO content_reports(target_kind,target_id,reporter_account_id,reporter_username,reason,details) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING RETURNING id`, req.Target.Kind, req.Target.Id, a.account, a.username, reason, req.Details).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `SELECT id FROM content_reports WHERE reporter_account_id=$1 AND target_kind=$2 AND target_id=$3 AND reason=$4`, a.account, req.Target.Kind, req.Target.Id, reason).Scan(&id)
			if err != nil {
				return nil, err
			}
			existing, e := readStoreReport(ctx, tx, id)
			if e != nil {
				return nil, e
			}
			// A role promotion may turn a previously ordinary pending report
			// into a staff decision. Decided reports remain durable duplicates.
			if !a.staff() || reason != "nsfw" || existing.Status != "pending" {
				return existing, nil
			}
		} else if err != nil {
			return nil, err
		}

		if a.staff() && reason == "nsfw" {
			if _, err = tx.ExecContext(ctx, `UPDATE content_reports SET status='confirmed_nsfw',reviewed_by=$2,reviewed_at=NOW() WHERE id=$1`, id, a.account); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE content_moderation SET staff_nsfw=true WHERE target_kind=$1 AND target_id=$2`, req.Target.Kind, req.Target.Id); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO content_report_actions(report_id,action,actor_account_id) VALUES($1,'confirm_nsfw',$2)`, id, a.account); err != nil {
				return nil, err
			}
			if err = auditStore(ctx, tx, a, req.GetActor().GetIp(), "moderation.report_nsfw", req.Target, map[string]any{"report_id": id, "reason": reason, "details": req.Details}); err != nil {
				return nil, err
			}
		}
		return readStoreReport(ctx, tx, id)
	})
}
func (s *Store) Reveal(ctx context.Context, req *pb.ActionRequest) (*pb.ActionResponse, error) {
	return s.mutate(ctx, req, func(tx *sql.Tx, a storeActor, c storeContent) (*pb.Report, error) {
		access, err := storeAccess(ctx, tx, a, req.Target, c, true)
		if err != nil {
			return nil, err
		}
		if Eligibility(true, a.birth, time.Now()) != "eligible" {
			return nil, status.Error(codes.PermissionDenied, "adult eligibility required")
		}
		if !access.IsNsfw {
			return nil, nil
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO content_reveals(target_kind,target_id,account_id,revision) VALUES($1,$2,$3,$4) ON CONFLICT(target_kind,target_id,account_id) DO UPDATE SET revision=EXCLUDED.revision,created_at=NOW() WHERE content_reveals.revision<>EXCLUDED.revision`, req.Target.Kind, req.Target.Id, a.account, access.Revision)
		return nil, err
	})
}
func (s *Store) SetAuthorLabel(ctx context.Context, req *pb.ActionRequest) (*pb.ActionResponse, error) {
	return s.mutate(ctx, req, func(tx *sql.Tx, a storeActor, c storeContent) (*pb.Report, error) {
		if a.id != c.owner {
			return nil, status.Error(codes.PermissionDenied, "only the owner may set the author label")
		}
		var old bool
		if err := tx.QueryRowContext(ctx, `SELECT author_nsfw FROM content_moderation WHERE target_kind=$1 AND target_id=$2`, req.Target.Kind, req.Target.Id).Scan(&old); err != nil {
			return nil, err
		}
		if old == req.IsNsfw {
			return nil, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE content_moderation SET author_nsfw=$3 WHERE target_kind=$1 AND target_id=$2`, req.Target.Kind, req.Target.Id, req.IsNsfw); err != nil {
			return nil, err
		}
		return nil, auditStore(ctx, tx, a, req.GetActor().GetIp(), "moderation.author_label", req.Target, map[string]any{"is_nsfw": req.IsNsfw})
	})
}
func (s *Store) ListReports(ctx context.Context, req *pb.ListRequest) (*pb.ListResponse, error) {
	if req == nil || req.Offset < 0 || req.Limit < 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid report page")
	}
	switch req.Status {
	case "", "pending", "action_taken", "dismissed", "reversed":
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid report status")
	}
	tx, err := s.transaction(ctx, true)
	if err != nil {
		return nil, storeFailure(err)
	}
	defer tx.Rollback()
	a, err := loadStoreActor(ctx, tx, req.GetActor().GetAccountId())
	if err != nil {
		return nil, storeFailure(err)
	}
	if !a.staff() {
		return nil, status.Error(codes.PermissionDenied, "moderation permission required")
	}
	filterStatus := req.Status
	if filterStatus == "action_taken" {
		filterStatus = "confirmed_nsfw"
	}
	limit := req.Limit
	if limit == 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	out := &pb.ListResponse{Reports: []*pb.Report{}}
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM content_reports WHERE ($1='' OR status=$1)`, filterStatus).Scan(&out.Total)
	if err != nil {
		return nil, storeFailure(err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+storeReportProjection+storeReportFrom+` WHERE ($1='' OR r.status=$1) ORDER BY r.created_at DESC,r.id DESC LIMIT $2 OFFSET $3`, filterStatus, limit, req.Offset)
	if err != nil {
		return nil, storeFailure(err)
	}
	for rows.Next() {
		r, e := scanStoreReport(rows)
		if e != nil {
			rows.Close()
			return nil, storeFailure(e)
		}
		out.Reports = append(out.Reports, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, storeFailure(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, storeFailure(err)
	}
	return out, nil
}
func (s *Store) ReviewReport(ctx context.Context, req *pb.ReviewRequest) (*pb.ActionResponse, error) {
	if req == nil || req.ReportId <= 0 || utf8.RuneCountInString(req.Note) > 10000 {
		return nil, status.Error(codes.InvalidArgument, "invalid report review")
	}
	switch req.Action {
	case "confirm_nsfw", "dismiss", "reverse":
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid review action")
	}
	tx, err := s.transaction(ctx, false)
	if err != nil {
		return nil, storeFailure(err)
	}
	defer tx.Rollback()
	a, err := loadStoreActor(ctx, tx, req.GetActor().GetAccountId())
	if err != nil {
		return nil, storeFailure(err)
	}
	if !a.staff() {
		return nil, status.Error(codes.PermissionDenied, "moderation permission required")
	}
	r, err := readStoreReport(ctx, tx, req.ReportId)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "report not found")
	}
	if err != nil {
		return nil, storeFailure(err)
	}
	if err = lockStoreRating(ctx, tx, r.Target); err != nil {
		return nil, storeFailure(err)
	}
	r, err = scanStoreReport(tx.QueryRowContext(ctx, `SELECT `+storeReportProjection+storeReportFrom+` WHERE r.id=$1 FOR UPDATE OF r`, req.ReportId))
	if err != nil {
		return nil, storeFailure(err)
	}
	var applied bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM content_report_actions WHERE report_id=$1 AND action=$2)`, req.ReportId, req.Action).Scan(&applied)
	if err != nil {
		return nil, storeFailure(err)
	}
	if !applied {
		next := ""
		switch req.Action {
		case "confirm_nsfw":
			if r.Status == "pending" {
				next = "confirmed_nsfw"
			}
		case "dismiss":
			if r.Status == "pending" {
				next = "dismissed"
			}
		case "reverse":
			if r.Status == "action_taken" {
				next = "reversed"
			}
		}
		if next == "" {
			return nil, status.Error(codes.FailedPrecondition, "report has already been decided")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE content_reports SET status=$2,reviewed_by=$3,reviewed_at=NOW(),review_note=$4 WHERE id=$1`, r.Id, next, a.account, req.Note); err != nil {
			return nil, storeFailure(err)
		}
		if req.Action != "dismiss" {
			if _, err = tx.ExecContext(ctx, `UPDATE content_moderation SET staff_nsfw=EXISTS(SELECT 1 FROM content_reports WHERE target_kind=$1 AND target_id=$2 AND status='confirmed_nsfw') WHERE target_kind=$1 AND target_id=$2`, r.Target.Kind, r.Target.Id); err != nil {
				return nil, storeFailure(err)
			}
			if req.Action == "reverse" {
				if err = clearStoreLegacy(ctx, tx, r.Target); err != nil {
					return nil, storeFailure(err)
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO content_report_actions(report_id,action,actor_account_id) VALUES($1,$2,$3)`, r.Id, req.Action, a.account); err != nil {
			return nil, storeFailure(err)
		}
		if err = auditStore(ctx, tx, a, req.GetActor().GetIp(), "moderation.review_"+req.Action, r.Target, map[string]any{"report_id": r.Id, "note": req.Note, "previous_status": r.Status, "status": next}); err != nil {
			return nil, storeFailure(err)
		}
		r, err = readStoreReport(ctx, tx, r.Id)
		if err != nil {
			return nil, storeFailure(err)
		}
	}
	c, visible, err := loadStoreContent(ctx, tx, a, r.Target)
	if err != nil {
		return nil, storeFailure(err)
	}
	access, err := storeAccess(ctx, tx, a, r.Target, c, visible)
	if err != nil {
		return nil, storeFailure(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, storeFailure(err)
	}
	return &pb.ActionResponse{Report: r, Access: access}, nil
}
func clearStoreLegacy(ctx context.Context, tx *sql.Tx, t *pb.Target) error {
	var query string
	switch t.Kind {
	case "blog":
		query = `DELETE FROM blog_admin_flags WHERE flag_type='nsfw' AND blog_id=(SELECT id FROM blog WHERE blog_id=$1)`
	case "event":
		query = `DELETE FROM event_admin_flags WHERE flag_type='nsfw' AND event_id=(SELECT id FROM events WHERE slug=$1)`
	case "group":
		query = `DELETE FROM group_admin_flags WHERE flag_type='nsfw' AND group_id=(SELECT id FROM groups WHERE slug=$1)`
	default:
		return nil
	}
	_, err := tx.ExecContext(ctx, query, t.Id)
	return err
}
