package main

// LOCAL TEST ONLY: opt in with MONKEYS_NSFW_BROWSER_FIXTURE=1 and a
// MODERATION_TEST_DSN URL for 127.0.0.1:55435/moderation_store_test.
// Run: go test ./microservices/the_monkeys_gateway -run '^TestNSFWBrowserFixture$' -count=1 -v -timeout=30m
// POST /api/v1/__nsfw_fixture/stop drops this run's schema. Automatic limit: 20m.
// Authentication alone is STUBBED: fixed local-fixture-<actor> tokens map to
// fixture_<actor> accounts; Validate reads current active status/roles from SQL.
// No real JWTs, email, production configuration or live service connections.
// /data is a manifest; use content_path for DTOs redacted by REAL middleware.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	authpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	commonmoderation "github.com/the-monkeys/the_monkeys/common/moderation"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/admin"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/moderation"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type nsfwFixtureAuth struct {
	authpb.UnimplementedAuthServiceServer
	db *sql.DB
}

func (a *nsfwFixtureAuth) Validate(ctx context.Context, req *authpb.ValidateRequest) (*authpb.ValidateResponse, error) {
	actor := strings.TrimPrefix(req.Token, "local-fixture-")
	switch actor {
	case "adult", "minor", "missing", "staff", "owner":
	default:
		return nil, status.Error(codes.Unauthenticated, "fixture token required")
	}
	if req.Token != "local-fixture-"+actor {
		return nil, status.Error(codes.Unauthenticated, "fixture token required")
	}
	out := &authpb.ValidateResponse{StatusCode: 200}
	err := a.db.QueryRowContext(ctx, `SELECT u.id,u.account_id,u.username,u.email,r.role_desc FROM user_account u JOIN user_role r ON r.id=u.role_id JOIN user_status s ON s.id=u.user_status WHERE u.account_id=$1 AND s.status='active'`, "fixture_"+actor).Scan(&out.UserId, &out.AccountId, &out.UserName, &out.Email, &out.Role)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "fixture identity unavailable")
	}
	return out, nil
}

func TestNSFWBrowserFixture(t *testing.T) {
	if os.Getenv("MONKEYS_NSFW_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in local browser fixture")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	u, err := url.Parse(os.Getenv("MODERATION_TEST_DSN"))
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.Port() != "55435" || u.Path != "/moderation_store_test" || u.Fragment != "" || u.User == nil {
		t.Fatal("require MODERATION_TEST_DSN for loopback port 55435, exact moderation_store_test database")
	}
	for key := range u.Query() {
		if key != "sslmode" && key != "connect_timeout" && key != "application_name" {
			t.Fatal("DSN connection overrides are forbidden")
		}
	}
	q := u.Query()
	q.Set("connect_timeout", "5")
	u.RawQuery = q.Encode()
	root, err := sql.Open("postgres", u.String())
	must(err)
	t.Cleanup(func() { root.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	must(root.PingContext(ctx))
	var extension bool
	must(root.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_trgm')`).Scan(&extension))
	if !extension {
		t.Fatal("isolated test DB must already have pg_trgm")
	}
	schema := "nsfw_browser_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = root.ExecContext(ctx, `CREATE SCHEMA `+schema)
	must(err)
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if _, err := root.ExecContext(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error("fixture schema cleanup:", err)
		}
	})
	// Cleanup order is LIFO: close scoped connections before dropping the schema.
	// root stays open through t.Cleanup (registered first, rather than defer).
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	must(err)
	db.SetMaxOpenConns(12)
	t.Cleanup(func() { db.Close() })
	exec := func(query string, args ...any) { t.Helper(); _, e := db.ExecContext(ctx, query, args...); must(e) }
	for _, migration := range []string{"000001_intital_relation_setup", "000005_add_storage_assets_table", "000010_add_events", "000011_meetup_communities", "000019_admin_moderation", "000022_blog_group_audience", "000023_discussions", "000024_discussion_group_audience", "000025_content_moderation"} {
		data, e := os.ReadFile(filepath.Join("..", "..", "schema", migration+".up.sql"))
		must(e)
		exec(string(data))
	}
	exec(`INSERT INTO user_status(status) VALUES('active') ON CONFLICT DO NOTHING;
 INSERT INTO user_role(role_desc) VALUES('Viewer'),('Support') ON CONFLICT DO NOTHING`)
	for _, actor := range []string{"adult", "minor", "missing", "staff", "owner"} {
		role, dob := "Viewer", "1990-01-01"
		if actor == "minor" {
			dob = "2010-01-01"
		}
		if actor == "missing" {
			dob = ""
		}
		if actor == "staff" {
			role = "Support"
		}
		exec(`INSERT INTO user_account(account_id,username,email,first_name,last_name,date_of_birth,role_id,user_status) VALUES($1::text,$1::text,$1::text||'@fixture.invalid','LOCAL TEST',$1::text,NULLIF($2,'')::date,(SELECT id FROM user_role WHERE role_desc=$3),(SELECT id FROM user_status WHERE status='active'))`, "fixture_"+actor, dob, role)
	}
	exec(`INSERT INTO blog(blog_id,user_id,status) SELECT 'browser-blog',id,'Published' FROM user_account WHERE account_id='fixture_owner';
 INSERT INTO groups(slug,name,description,organizer_id,status,visibility) SELECT 'browser-group','LOCAL TEST group','LOCAL TEST safe body',id,'published','public' FROM user_account WHERE account_id='fixture_owner';
 INSERT INTO events(slug,title,description,organizer_id,start_time,end_time,event_type,status,visibility) SELECT 'browser-event','LOCAL TEST event','LOCAL TEST safe body',id,NOW()+interval '1 day',NOW()+interval '25 hours','virtual','published','public' FROM user_account WHERE account_id='fixture_owner';
 INSERT INTO discussion_posts(public_id,author_id,body,edited_until) SELECT 'browser-discussion',id,'LOCAL TEST safe body',NOW()+interval '20 minutes' FROM user_account WHERE account_id='fixture_owner'`)
	store := commonmoderation.NewStore(db)
	wire := bufconn.Listen(1024 * 1024)
	rpc := grpc.NewServer()
	pb.RegisterModerationServiceServer(rpc, commonmoderation.NewServer(store))
	authpb.RegisterAuthServiceServer(rpc, &nsfwFixtureAuth{db: db})
	go rpc.Serve(wire)
	t.Cleanup(func() { rpc.Stop(); wire.Close() })
	conn, err := grpc.NewClient("passthrough:///nsfw-browser-fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithNoProxy(), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return wire.DialContext(ctx) }))
	must(err)
	t.Cleanup(func() { conn.Close() })
	client := pb.NewModerationServiceClient(conn)
	log := zap.NewNop().Sugar()
	authClient := &auth.ServiceClient{Client: authpb.NewAuthServiceClient(conn), Log: log}
	identity := auth.InitAuthMiddleware(authClient, log)
	policy := moderation.New(client)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	must(router.SetTrustedProxies(nil))
	router.Use(gin.Recovery(), func(c *gin.Context) {
		// Support local frontend origins only, including credentialed fetches.
		if origin := c.GetHeader("Origin"); origin != "" {
			parsed, e := url.Parse(origin)
			if e != nil || parsed.Scheme != "http" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
				c.AbortWithStatus(403)
				return
			}
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
			c.Header("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, OPTIONS")
		}
		c.Header("Cache-Control", "private, no-store")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}, policy.Middleware(identity.LoadOptionalIdentity))
	policy.RegisterRoutes(router, identity)
	staff := &admin.AdminServiceClient{Moderation: client}
	staff.RegisterModerationRoutes(router, authClient, policy, log)
	router.GET("/api/v1/admin/session", admin.LocalNetworkMiddleware(log), identity.AuthRequired, admin.RequirePermission(log, staffaccess.Access), staff.StaffSession)
	router.GET("/api/v1/auth/validate-session", identity.AuthRequired, func(c *gin.Context) {
		// Flat IUser DTO expected by real validateSession(), useAuth and root Navbar.
		c.JSON(200, gin.H{"status_code": 200, "account_id": c.GetString("accountId"), "username": c.GetString("userName"), "first_name": "LOCAL TEST", "last_name": "Fixture", "email": c.GetString("userName") + "@fixture.invalid", "email_verification_status": "verified"})
	})
	profile := func(c *gin.Context) {
		if c.Param("username") != c.GetString("userName") {
			c.AbortWithStatus(403)
			return
		}
		if c.Request.Method == "PUT" {
			var body struct {
				Values struct {
					DOB *string `json:"date_of_birth"`
				} `json:"values"`
			}
			if c.ShouldBindJSON(&body) != nil || body.Values.DOB == nil {
				c.AbortWithStatus(400)
				return
			}
			dob := *body.Values.DOB
			date, e := time.Parse("2006-01-02", dob)
			if dob != "" && (e != nil || date.After(time.Now())) {
				c.AbortWithStatus(400)
				return
			}
			if _, e = db.ExecContext(c.Request.Context(), `UPDATE user_account SET date_of_birth=NULLIF($1,'')::date WHERE account_id=$2`, dob, c.GetString("accountId")); e != nil {
				c.AbortWithStatus(500)
				return
			}
		}
		var dob string
		if e := db.QueryRowContext(c.Request.Context(), `SELECT COALESCE(to_char(date_of_birth,'YYYY-MM-DD'),'') FROM user_account WHERE account_id=$1`, c.GetString("accountId")).Scan(&dob); e != nil {
			c.AbortWithStatus(500)
			return
		}
		c.JSON(200, gin.H{"account_id": c.GetString("accountId"), "username": c.GetString("userName"), "first_name": "LOCAL TEST", "last_name": "Fixture", "date_of_birth": dob, "bio": "LOCAL TEST", "address": "", "contact_number": "", "twitter": "", "github": "", "linkedin": "", "instagram": "", "user_status": "active", "created_at": gin.H{"seconds": time.Now().Unix(), "nanos": 0}, "updated_at": gin.H{"seconds": time.Now().Unix(), "nanos": 0}})
	}
	router.GET("/api/v1/user/:username", identity.AuthRequired, profile)
	router.PUT("/api/v1/user/:username", identity.AuthRequired, profile)
	router.POST("/api/v1/__nsfw_fixture/session", func(c *gin.Context) {
		var body struct {
			Actor string `json:"actor"`
		}
		if c.ShouldBindJSON(&body) != nil {
			c.AbortWithStatus(400)
			return
		}
		switch body.Actor {
		case "anonymous", "adult", "minor", "missing", "staff", "owner":
		default:
			c.AbortWithStatus(400)
			return
		}
		token, age := "local-fixture-"+body.Actor, 1200
		if body.Actor == "anonymous" {
			token, age = "", -1
		}
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("mat", token, age, "/", "", false, true)
		c.JSON(200, gin.H{"actor": body.Actor})
	})
	targets := []gin.H{}
	for i, kind := range []string{"blog", "event", "group", "discussion"} {
		id, contentPath := "browser-"+kind, "/api/v1/"+kind+"s/browser-"+kind
		if kind == "blog" {
			contentPath = "/api/v2/blog/" + id
		}
		img := image.NewRGBA(image.Rect(0, 0, 64, 40))
		for y := 0; y < 40; y++ {
			for x := 0; x < 64; x++ {
				img.SetRGBA(x, y, color.RGBA{uint8(40 + i*45), uint8(100 + x), uint8(80 + y), 255})
			}
		}
		var buffer bytes.Buffer
		must(png.Encode(&buffer, img))
		data := append([]byte(nil), buffer.Bytes()...)
		checksum := fmt.Sprintf("%x", sha256.Sum256(data))
		key := "assets/sha256/" + checksum[:2] + "/" + checksum[2:4] + "/" + checksum + ".png"
		ref := uuid.NewString()
		exec(`INSERT INTO storage_assets(checksum,object_key,content_type,size,width,height) VALUES($1,$2,'image/png',$3,64,40)`, checksum, key, len(data))
		exec(`INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,$3,$4,'fixture_image','safe.png')`, ref, checksum, kind, id)
		legacy := "posts/" + id + "/safe.png"
		if kind == "event" {
			legacy = "events/" + id + "/cover"
			exec(`UPDATE events SET cover_image=$1 WHERE slug=$2`, "/api/v2/storage/"+legacy, id)
		}
		if kind == "group" {
			legacy = "groups/" + id + "/logo"
			exec(`UPDATE groups SET logo_image=$1,cover_image=$1 WHERE slug=$2`, "/api/v2/storage/"+legacy, id)
		}
		if kind == "discussion" {
			legacy = key
			exec(`INSERT INTO discussion_files(post_id,storage_key,content_type) SELECT id,$1,'image/png' FROM discussion_posts WHERE public_id=$2`, key, id)
		}
		media := "/api/v2/storage/" + legacy
		raw := gin.H{"title": "LOCAL TEST " + kind, "name": "LOCAL TEST " + kind, "body": "LOCAL TEST safe body", "description": "LOCAL TEST safe description", "author_username": "fixture_owner", "owner_account_id": "fixture_owner", "organizer_username": "fixture_owner", "organizer_account_id": "fixture_owner", "status": "published", "audience": "public", "first_image": media, "first_paragraph": "LOCAL TEST safe body", "cover_image": media, "logo_image": media, "blog": gin.H{"blocks": []any{gin.H{"id": "fixture-text", "type": "paragraph", "data": gin.H{"text": "LOCAL TEST safe body"}}, gin.H{"id": "fixture-image", "type": "image", "data": gin.H{"file": gin.H{"url": media}, "caption": "LOCAL TEST safe PNG"}}}}, "files": []any{gin.H{"storage_key": key, "url": media, "content_type": "image/png"}}}
		switch kind {
		case "blog":
			raw["blog_id"] = id
		case "discussion":
			raw["public_id"] = id
		default:
			raw["slug"] = id
		}
		router.GET(contentPath, func(c *gin.Context) { c.JSON(200, raw) })
		serveImage := func(c *gin.Context) {
			// Real moderation middleware must set this from CheckMedia before any bytes.
			if authorized, _ := c.Get(moderation.MediaAuthorizationKey); authorized != true {
				c.AbortWithStatus(404)
				return
			}
			// Deliberately simulate a cacheable storage origin: actual policy must override.
			c.Header("Cache-Control", "public, max-age=86400")
			c.Header("ETag", `"`+checksum+`"`)
			c.Data(200, "image/png", data)
		}
		paths := map[string]bool{}
		for _, prefix := range []string{"/api/v2/storage/", "/api/storage/"} {
			paths[prefix+legacy] = true
			paths[prefix+key] = true
		}
		for path := range paths {
			router.GET(path, serveImage)
			router.HEAD(path, serveImage)
		}
		targets = append(targets, gin.H{"kind": kind, "id": id, "content_path": contentPath, "media_path": media, "media_alias": "/api/storage/" + legacy, "cas_path": "/api/v2/storage/" + key, "cas_alias": "/api/storage/" + key, "checksum": checksum, "ref_id": ref, "initial_nsfw": kind != "blog"})
		if kind != "blog" {
			_, e := client.SetAuthorLabel(ctx, &pb.ActionRequest{Actor: &pb.Actor{AccountId: "fixture_owner"}, Target: &pb.Target{Kind: kind, Id: id}, IsNsfw: true})
			must(e)
		}
	}
	router.GET("/api/v1/__nsfw_fixture/data", func(c *gin.Context) {
		c.JSON(200, gin.H{"local_test": true, "schema": schema, "actors": []string{"anonymous", "adult", "minor", "missing", "staff", "owner"}, "targets": targets})
	})
	stop := make(chan struct{})
	var once sync.Once
	router.POST("/api/v1/__nsfw_fixture/stop", func(c *gin.Context) { c.JSON(200, gin.H{"stopping": true}); once.Do(func() { close(stop) }) })
	listener, err := net.Listen("tcp4", "127.0.0.1:8085")
	must(err)
	server := &http.Server{Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() { server.Close() })
	// Confirm HTTP is serving before emitting the sole fixture readiness line.
	probe := &http.Client{Timeout: 5 * time.Second}
	response, err := probe.Get("http://127.0.0.1:8085/api/v1/__nsfw_fixture/data")
	must(err)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("fixture readiness failed")
	}
	fmt.Println("FIXTURE_READY http://127.0.0.1:8085")
	signals, done := signal.NotifyContext(context.Background(), os.Interrupt)
	defer done()
	timer := time.NewTimer(20 * time.Minute)
	defer timer.Stop()
	select {
	case <-stop:
	case <-timer.C:
	case <-signals.Done():
	case err := <-serveErr:
		if err != http.ErrServerClosed {
			t.Error(err)
		}
	}
	shutdown, finish := context.WithTimeout(context.Background(), 5*time.Second)
	defer finish()
	must(server.Shutdown(shutdown))
}
