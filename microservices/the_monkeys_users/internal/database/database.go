package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UserDb interface {
	// Create queries
	CreateUserLog(user *models.UserLogs, description string) error
	AddBlogWithId(models.TheMonkeysMessage) error
	AddUserInterest(interest []string, username string) error
	AddPermissionToAUser(blogId string, userId int64, inviterID string, permissionType string) error
	CreateNewTopics(topics []string, category, username string) error
	BookMarkABlog(blogId string, userId int64) error
	FollowAUser(followingUsername, followersUsername string) error
	LikeBlog(username string, blogID string) error
	InsertTopicWithCategory(ctx context.Context, description, category string) error

	// Get queries
	CheckIfEmailExist(email string) (*models.TheMonkeysUser, error)
	CheckIfUsernameExist(username string) (*models.TheMonkeysUser, error)
	CheckIfAccIdExist(accountId string) (*models.TheMonkeysUser, error)
	GetMyProfile(username string) (*models.UserProfileRes, error)
	GetUserProfile(username string) (*models.UserAccount, error)
	GetAllTopicsFromDb() (*pb.GetTopicsResponse, error)
	GetAllCategories() (*pb.GetAllCategoriesRes, error)
	GetUserActivities(userId int64) (*pb.UserActivityResp, error)
	GetUserInterest(username string) ([]string, error)
	GetBlogsByUserName(username string) (*pb.BlogsByUserNameRes, error)
	GetBlogsByUserIdWithEditorAccess(accountId int64) (*pb.BlogsByUserNameRes, error)
	GetBlogsByAccountId(accountId string) (*pb.BlogsByUserNameRes, error)
	GetCoAuthorBlogsByAccountId(accountId string) (*pb.BlogsByUserNameRes, error)
	GetBookmarkBlogsByAccountId(accountId string) (*pb.BlogsByUserNameRes, error)
	GetFollowings(username string) ([]models.TheMonkeysUser, error)
	GetFollowers(username string) ([]models.TheMonkeysUser, error)
	GetBlogsByBlogId(blogId string) (models.Blog, error)
	IsUserFollowing(followerUsername string, followingUsername string) (bool, error)
	IsBlogLikedByUser(username string, blogId string) (bool, error)
	IsBlogBookmarkedByUser(username string, blogId string) (bool, error)
	CountBlogBookmarks(blogId string) (int64, error)
	GetBookmarkBlogsByUsername(username string) ([]models.Blog, error)
	GetBlogLikeCount(blogId string) (int64, error)
	FindUsersWithPagination(searchTerm string, limit int, offset int) ([]models.UserAccount, error)
	FindUsersV2(ctx context.Context, searchTerm string, limit, offset int) ([]models.UserAccount, error)
	GetFollowersAndFollowingsCounts(username string) (int, int, error)
	GetBlogByBlogId(blogId string) (*models.Blog, error)
	GetUsersByAccountIds(accIds []string) ([]models.UserProfileRes, error)
	// Update queries
	UpdateUserProfile(username string, dbUserInfo *models.UserProfileRes) error
	UpdateBlogStatusToPublish(blogId string, status string) error
	UpdateBlogStatusToDraft(blogId string, status string) error

	// Business card queries
	CreateBusinessCard(card *models.BusinessCard) (*models.BusinessCard, error)
	GetBusinessCard(accountId, id string) (*models.BusinessCard, error)
	ListBusinessCards(accountId string, limit, offset int) ([]models.BusinessCard, error)
	UpdateBusinessCard(card *models.BusinessCard) (*models.BusinessCard, error)
	DeleteBusinessCard(accountId, id string) error

	// Private address (address book) queries
	CreateUserAddress(addr *models.UserAddress) (*models.UserAddress, error)
	GetUserAddress(accountId, id string) (*models.UserAddress, error)
	ListUserAddresses(accountId string) ([]models.UserAddress, error)
	UpdateUserAddress(addr *models.UserAddress) (*models.UserAddress, error)
	DeleteUserAddress(accountId, id string) error

	// Delete queries
	GetOwnedBlogIDs(username string) ([]string, error)
	DeleteUserProfile(username string) error
	RemoveUserInterest(interests []string, username string) error
	RevokeBlogPermissionFromAUser(blogId string, userId int64, permissionType string) error
	RemoveBookmarkFromBlog(blogId string, userId int64) error
	DeleteBlogAndReferences(blogId string) error
	UnFollowAUser(followingUsername, followersUsername string) error
	UnlikeBlog(username string, blogID string) error
}

type uDBHandler struct {
	db  *sql.DB
	log *zap.SugaredLogger
}

// NewUserDbHandler initializes the database with connection pooling
func NewUserDbHandler(cfg *config.Config, log *zap.SugaredLogger) (UserDb, error) {
	url := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.Postgresql.PrimaryDB.DBUsername,
		cfg.Postgresql.PrimaryDB.DBPassword,
		cfg.Postgresql.PrimaryDB.DBHost,
		cfg.Postgresql.PrimaryDB.DBPort,
		cfg.Postgresql.PrimaryDB.DBName,
	)
	db, err := sql.Open("pgx", url)
	if err != nil {
		log.Fatalf("Cannot connect to PostgreSQL using pgx driver, error: %+v", err)
		return nil, err
	}

	// Configure connection pooling
	db.SetMaxOpenConns(25)                 // Maximum number of open connections
	db.SetMaxIdleConns(10)                 // Maximum number of idle connections
	db.SetConnMaxLifetime(5 * time.Minute) // Connection lifetime limit

	if err = db.Ping(); err != nil {
		log.Errorf("Ping test failed for PostgreSQL, error: %+v", err)
		return nil, err
	}

	return &uDBHandler{db: db, log: log}, nil
}

// To get User Profile
func (uh *uDBHandler) GetUserProfile(username string) (*models.UserAccount, error) {
	var tmu models.UserAccount

	// Step 1: Fetch user profile information from the user_account table
	err := uh.db.QueryRow(`
		SELECT id, username, first_name, last_name, bio, avatar_url, created_at, address, linkedin, github, twitter, instagram 
		FROM user_account WHERE username = $1;`, username).
		Scan(&tmu.Id, &tmu.UserName, &tmu.FirstName, &tmu.LastName, &tmu.Bio, &tmu.AvatarUrl, &tmu.CreatedAt,
			&tmu.Address, &tmu.LinkedIn, &tmu.Github, &tmu.Twitter, &tmu.Instagram)

	if err != nil {
		uh.log.Errorf("Can't find a user with username %s, error: %+v", username, err)
		return nil, err
	}

	// Step 2: Fetch user's interests by joining user_interest and topics tables
	rows, err := uh.db.Query(`
		SELECT t.description 
		FROM topics t
		JOIN user_interest ui ON t.id = ui.topics_id
		WHERE ui.user_id = $1;`, tmu.Id)

	if err != nil {
		uh.log.Errorf("Error fetching interests for user ID %d, error: %+v", tmu.Id, err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			uh.log.Errorf("Error closing rows for user ID %d, error: %+v", tmu.Id, err)
		}
	}()

	// Step 3: Collect the interests into the UserAccount struct
	var interests []string
	for rows.Next() {
		var interest string
		if err := rows.Scan(&interest); err != nil {
			uh.log.Errorf("Error scanning interest for user ID %d, error: %+v", tmu.Id, err)
			return nil, err
		}
		interests = append(interests, interest)
	}

	// Assign interests to the user's profile
	tmu.Interests = interests

	uh.log.Debugf("Successfully fetched profile and interests for user: %s", username)
	return &tmu, nil
}

func (uh *uDBHandler) CheckIfEmailExist(email string) (*models.TheMonkeysUser, error) {
	return uh.fetchUserByIdentifier("email", email)
}

func (uh *uDBHandler) CheckIfUsernameExist(username string) (*models.TheMonkeysUser, error) {
	return uh.fetchUserByIdentifier("username", username)
}

func (uh *uDBHandler) GetMyProfile(username string) (*models.UserProfileRes, error) {
	var profile models.UserProfileRes

	// Step 1: Fetch user profile information
	err := uh.db.QueryRow(`
		SELECT ua.account_id, ua.username, ua.first_name, ua.last_name, ua.email, ua.date_of_birth,
		ua.bio, ua.avatar_url, ua.created_at, ua.updated_at, ua.address, ua.contact_number, us.status,
		ua.view_permission, ua.linkedin, ua.github, ua.twitter, ua.instagram 
		FROM user_account ua
		INNER JOIN user_status us ON us.id = ua.user_status
		WHERE ua.username = $1;
	`, username).
		Scan(&profile.AccountId, &profile.Username, &profile.FirstName, &profile.LastName, &profile.Email,
			&profile.DateOfBirth, &profile.Bio, &profile.AvatarUrl, &profile.CreatedAt, &profile.UpdatedAt,
			&profile.Address, &profile.ContactNumber, &profile.UserStatus, &profile.ViewPermission,
			&profile.LinkedIn, &profile.Github, &profile.Twitter, &profile.Instagram)

	if err != nil {
		uh.log.Errorf("can't find a user profile with username %s, error: %+v", username, err)
		return nil, err
	}

	// Step 2: Fetch user's interests by joining user_interest and topics tables
	rows, err := uh.db.Query(`
		SELECT t.description 
		FROM topics t
		JOIN user_interest ui ON t.id = ui.topics_id
		JOIN user_account ua ON ua.id = ui.user_id
		WHERE ua.username = $1;
	`, username)

	if err != nil {
		uh.log.Errorf("Error fetching interests for username %s, error: %+v", username, err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			uh.log.Errorf("Error closing rows for username %s, error: %+v", username, err)
		}
	}()

	// Step 3: Collect the interests into the UserProfileRes struct
	var interests []string
	for rows.Next() {
		var interest string
		if err := rows.Scan(&interest); err != nil {
			uh.log.Errorf("Error scanning interest for user %s, error: %+v", username, err)
			return nil, err
		}
		interests = append(interests, interest)
	}

	// Assign the collected interests to the profile
	profile.Interests = interests

	uh.log.Debugf("Successfully fetched profile and interests for user: %s", username)
	return &profile, nil
}

func (uh *uDBHandler) UpdateUserProfile(username string, dbUserInfo *models.UserProfileRes) error {
	uh.log.Debugf("Starting profile update for user: %s", username)

	tx, err := uh.db.Begin()
	if err != nil {
		uh.log.Errorf("cannot begin transaction for user %s, error: %v", username, err)
		return status.Errorf(codes.Internal, "internal server error, error: %v", err)
	}

	stmt, err := tx.Prepare(`
		UPDATE user_account
		SET 
			first_name = $1,
			last_name = $2,
			date_of_birth = $3,
			bio = $4,
			updated_at = now(),
			address = $5,
			contact_number = $6,
			linkedin = $7,
			github = $8,
			twitter = $9,
			instagram = $10 
		WHERE username = $11;
	`)
	if err != nil {
		uh.log.Errorf("cannot prepare the update user query for user %s, error: %v", username, err)
		return status.Errorf(codes.Internal, "internal server error, error: %v", err)
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			uh.log.Errorf("cannot close the prepared statement for user %s, error: %v", username, err)
		}
	}()

	result := stmt.QueryRow(dbUserInfo.FirstName, dbUserInfo.LastName, dbUserInfo.DateOfBirth.Time,
		dbUserInfo.Bio.String, dbUserInfo.Address.String, dbUserInfo.ContactNumber.String,
		dbUserInfo.LinkedIn.String, dbUserInfo.Github.String, dbUserInfo.Twitter.String,
		dbUserInfo.Instagram.String, username)
	if result.Err() != nil {
		uh.log.Errorf("cannot update user %s, error: %v", username, result.Err())
		if pgErr, ok := result.Err().(*pgconn.PgError); ok && pgErr.Code == "23505" {
			return status.Errorf(codes.AlreadyExists, "email already exists")
		}
		return status.Errorf(codes.Internal, "internal server error, error: %v", result.Err())
	}

	err = tx.Commit()
	if err != nil {
		uh.log.Errorf("cannot commit the update profile for user %s, error: %v", username, err)
		return status.Errorf(codes.Internal, "internal server error, error: %v", err)
	}

	uh.log.Debugf("Successfully updated profile for user: %s", username)
	return nil
}

// GetOwnedBlogIDs returns the blog_id strings for all blogs owned by the given username.
// Must be called BEFORE DeleteUserProfile so the rows still exist.
func (uh *uDBHandler) GetOwnedBlogIDs(username string) ([]string, error) {
	rows, err := uh.db.Query(
		`SELECT b.blog_id FROM blog b
		 JOIN user_account ua ON b.user_id = ua.id
		 WHERE ua.username = $1`, username)
	if err != nil {
		return nil, fmt.Errorf("failed to query owned blog IDs for %s: %w", username, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan blog_id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return ids, nil
}

func (uh *uDBHandler) DeleteUserProfile(username string) error {
	tx, err := uh.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			uh.log.Errorf("Failed to rollback transaction for deleting user profile %s, error: %+v", username, err)
		}
	}()

	var id int64
	// Step 1: Fetch the user ID using the username from the user_account table
	if err := tx.QueryRow(`SELECT id FROM user_account WHERE username = $1`, username).Scan(&id); err != nil {
		uh.log.Errorf("Can't get ID for username %s, error: %+v", username, err)
		return err
	}

	// Step 2: Delete user-related data in the correct order to avoid constraint violations
	// Delete blog likes by the user
	_, err = tx.Exec(`DELETE FROM blog_likes WHERE user_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete blog likes for user ID %d, error: %+v", id, err)
		return err
	}

	// Delete blog comments by the user
	_, err = tx.Exec(`DELETE FROM blog_comments WHERE user_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete blog comments for user ID %d, error: %+v", id, err)
		return err
	}

	// Delete blog permissions and co-author permissions related to the user's blogs
	_, err = tx.Exec(`DELETE FROM blog_permissions WHERE blog_id IN (SELECT id FROM blog WHERE user_id = $1)`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete blog permissions for user ID %d, error: %+v", id, err)
		return err
	}

	_, err = tx.Exec(`DELETE FROM co_author_permissions WHERE blog_id IN (SELECT id FROM blog WHERE user_id = $1)`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete co-author permissions for user ID %d, error: %+v", id, err)
		return err
	}

	// Delete co-author invites related to the user's blogs
	_, err = tx.Exec(`DELETE FROM co_author_invites WHERE blog_id IN (SELECT id FROM blog WHERE user_id = $1)`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete co-author invites for user ID %d, error: %+v", id, err)
		return err
	}

	// Delete blogs owned by the user
	_, err = tx.Exec(`DELETE FROM blog WHERE user_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete blogs for user ID %d, error: %+v", id, err)
		return err
	}

	// Step 3: Remove any references where the user is a co-author or invited in someone else's blog
	_, err = tx.Exec(`DELETE FROM co_author_permissions WHERE co_author_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete co-author references for user ID %d, error: %+v", id, err)
		return err
	}

	_, err = tx.Exec(`DELETE FROM co_author_invites WHERE invitee_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete co-author invites for user ID %d, error: %+v", id, err)
		return err
	}

	// Step 4: Delete user interests
	_, err = tx.Exec(`DELETE FROM user_interest WHERE user_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete user interests for user ID %d, error: %+v", id, err)
		return err
	}

	// Step 5: Delete topics created by the user
	_, err = tx.Exec(`DELETE FROM topics WHERE user_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete topics for user ID %d, error: %+v", id, err)
		return err
	}

	// Step 6: Delete blog bookmarks created by the user
	_, err = tx.Exec(`DELETE FROM blog_bookmarks WHERE user_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete bookmarks for user ID %d, error: %+v", id, err)
		return err
	}

	// Step 7: Delete user authentication information
	_, err = tx.Exec(`DELETE FROM user_auth_info WHERE user_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete user authentication info for user ID %d, error: %+v", id, err)
		return err
	}

	// Step 8: Delete user notifications and preferences
	_, err = tx.Exec(`DELETE FROM user_notification_preferences WHERE user_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete notification preferences for user ID %d, error: %+v", id, err)
		return err
	}

	_, err = tx.Exec(`DELETE FROM notifications WHERE user_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete notifications for user ID %d, error: %+v", id, err)
		return err
	}

	// Step 9: Delete user follows (followers and following relationships)
	_, err = tx.Exec(`DELETE FROM user_follows WHERE follower_id = $1 OR following_id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete user follows for user ID %d, error: %+v", id, err)
		return err
	}

	// Step 10: Delete user account itself
	_, err = tx.Exec(`DELETE FROM user_account WHERE id = $1`, id)
	if err != nil {
		uh.log.Errorf("Failed to delete user account for user ID %d, error: %+v", id, err)
		return err
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		uh.log.Errorf("Failed to commit transaction for deleting user account %d, error: %+v", id, err)
		return err
	}

	uh.log.Debugf("Successfully deleted user profile for user: %s and all related data", username)
	return nil
}

// Write a function to create a user log user_account_log
func (uh *uDBHandler) AddUserLog(username string, ip string, description string, clientName string) error {
	var userId int64
	var clientId int8
	//From username find user id
	if err := uh.db.QueryRow(`
			SELECT id FROM user_account WHERE username = $1;`, username).Scan(&userId); err != nil {
		uh.log.Errorf("can't get id by using username %s, error: %+v", username, err)
		return nil
	}

	//From client name find client id
	if err := uh.db.QueryRow(`
			SELECT id FROM clients WHERE c_name = $1;`, clientName).Scan(&clientId); err != nil {
		uh.log.Errorf("can't get id by using client name %s, error: %+v", clientName, err)
		return nil
	}

	//Add a user log to user_account_log table
	stmt, err := uh.db.Prepare(`INSERT INTO user_account_log (user_id, ip_address, description, client_id) VALUES ($1, $2, $3, $4)`)
	if err != nil {
		uh.log.Errorf("cannot prepare statement to add user log into the user_account_log: %v", err)
		return err
	}

	defer func() {
		if err := stmt.Close(); err != nil {
			uh.log.Errorf("cannot close the prepared statement for user %s, error: %v", username, err)
		}
	}()

	row := stmt.QueryRow(userId, ip, description, clientId)
	if row.Err() != nil {
		uh.log.Errorf("cannot execute query to log user into user_account_log: %v", row.Err())
		return row.Err()
	}

	return nil
}

func (uh *uDBHandler) GetAllTopicsFromDb() (*pb.GetTopicsResponse, error) {
	resp := &pb.GetTopicsResponse{}
	topics := []*pb.Topics{}
	rows, err := uh.db.Query("SELECT description, category FROM topics")
	if err != nil {
		// Check if the error is "not found" or "internal server error" and return accordingly
		if errors.Is(err, sql.ErrNoRows) {
			return nil, constants.ErrNotFound
		}
		return nil, constants.ErrInternal
	}
	defer func() {
		if err := rows.Close(); err != nil {
			uh.log.Errorf("Error closing rows in GetAllTopicsFromDb: %v", err)
		}
	}()

	var topic, category string
	for rows.Next() {
		err := rows.Scan(&topic, &category)
		if err != nil {
			return nil, err
		}
		topics = append(topics, &pb.Topics{
			Topic:    topic,
			Category: category,
		})
	}

	resp.Topics = topics
	return resp, nil
}
func (uh *uDBHandler) GetAllCategories() (*pb.GetAllCategoriesRes, error) {
	resp := &pb.GetAllCategoriesRes{}
	categories := make(map[string]*pb.Category)
	rows, err := uh.db.Query("SELECT  DISTINCT description, category FROM topics")
	if err != nil {
		// Check if the error is "not found" or "internal server error" and return accordingly
		if errors.Is(err, sql.ErrNoRows) {
			return nil, constants.ErrNotFound
		}
		return nil, constants.ErrInternal
	}
	defer func() {
		if err := rows.Close(); err != nil {
			uh.log.Errorf("Error closing rows in GetAllCategories: %v", err)
		}
	}()

	var Description, category string
	for rows.Next() {
		err := rows.Scan(&Description, &category)
		if err != nil {
			return nil, err
		}
		if _, ok := categories[category]; !ok {
			categories[category] = &pb.Category{
				Topics: make([]string, 0), // Initialize Topics slice for the category
			}
		}

		// Append Description to the Topics slice of the corresponding category
		categories[category].Topics = append(categories[category].Topics, Description)
	}

	// Assign the map to resp.Categories
	resp.Category = categories
	return resp, nil
}

func (uh *uDBHandler) AddBlogWithId(msg models.TheMonkeysMessage) error {
	tx, err := uh.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
				uh.log.Errorf("rollback failed while adding blog %s: %v", msg.BlogId, rbErr)
			}
		}
	}()

	var userId int64
	//From account_id find user_id
	err = tx.QueryRow(`SELECT id FROM user_account WHERE account_id = $1;`, msg.AccountId).Scan(&userId)
	if err != nil {
		uh.log.Errorf("can't get id by using user_account %s, error: %+v", msg.AccountId, err)
		return fmt.Errorf("cannot find user by account_id %s: %w", msg.AccountId, err)
	}

	stmt, err := tx.Prepare(`INSERT INTO blog (user_id, blog_id, status) VALUES ($1, $2, $3) RETURNING id;`)
	if err != nil {
		uh.log.Errorf("cannot prepare statement to add blog into the blog: %v", err)
		return err
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			uh.log.Errorf("cannot close the prepared statement for user %s, error: %v", msg.AccountId, err)
		}
	}()

	var blogId int64
	err = stmt.QueryRow(userId, msg.BlogId, msg.BlogStatus).Scan(&blogId)
	if err != nil {
		uh.log.Errorf("cannot execute query to add blog into the blog: %v", err)
		return err
	}

	stmt2, err := tx.Prepare(`INSERT INTO blog_permissions (blog_id, user_id, permission_type) VALUES ($1, $2, $3);`)
	if err != nil {
		uh.log.Errorf("cannot prepare statement to add permission into the blog_permissions: %v", err)
		return err
	}
	defer func() {
		if closeErr := stmt2.Close(); closeErr != nil {
			uh.log.Errorf("cannot close the permission prepared statement for user %s, error: %v", msg.AccountId, closeErr)
		}
	}()

	if _, err = stmt2.Exec(blogId, userId, constants.RoleOwner); err != nil {
		uh.log.Errorf("cannot execute query to add permission into the blog_permissions: %v", err)
		return err
	}

	err = tx.Commit()
	if err != nil {
		uh.log.Errorf("cannot commit the add blog for user %s, error: %v", msg.AccountId, err)
		return err
	}

	return nil
}

func (uh *uDBHandler) GetUserActivities(userId int64) (*pb.UserActivityResp, error) {
	uh.log.Debugf("Retrieving user activity for: %v", userId)
	activities := []*pb.UserActivity{}
	rows, err := uh.db.Query("SELECT description, timestamp FROM user_account_log WHERE user_id = $1 ORDER BY timestamp DESC;", userId)
	if err != nil {
		uh.log.Errorf("error retrieving user activities for user id %v, err: %v", userId, err)
		return nil, status.Errorf(codes.Internal, "cannot get the user activity")
	}
	defer func() {
		if err := rows.Close(); err != nil {
			uh.log.Errorf("error closing rows for user id %v, err: %v", userId, err)
		}
	}()

	for rows.Next() {
		var desc, timestamp string
		err := rows.Scan(&desc, &timestamp)
		if err != nil {
			uh.log.Errorf("cannot scan the user activity, err: %v", err)
			return nil, status.Errorf(codes.Internal, "cannot scan the user activity")
		}
		activities = append(activities, &pb.UserActivity{
			Description: desc,
			Timestamp:   timestamp,
		})
	}

	if err = rows.Err(); err != nil {
		uh.log.Errorf("error iterating over rows, err: %v", err)
		return nil, status.Errorf(codes.Internal, "error iterating over rows")
	}

	if len(activities) == 0 {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("activity for user id %v doesn't exist", userId))
	}

	return &pb.UserActivityResp{
		Response: activities,
	}, nil
}

func (uh *uDBHandler) UpdateBlogStatusToPublish(blogId string, status string) error {
	uh.log.Debugf("the blog %v is being published", blogId)
	row := uh.db.QueryRow("UPDATE blog SET status = $1 WHERE blog_id = $2", status, blogId)
	if row.Err() != nil {
		return row.Err()
	}

	uh.log.Debugf("the blog %v is successfully published", blogId)
	return nil
}

func (uh *uDBHandler) CheckIfAccIdExist(accountId string) (*models.TheMonkeysUser, error) {
	return uh.fetchUserByIdentifier("account_id", accountId)
}

// TODO: Find all the fields of models.TheMonkeysUser
func (uh *uDBHandler) fetchUserByIdentifier(identifierType, identifierValue string) (*models.TheMonkeysUser, error) {
	var tmu models.TheMonkeysUser
	query := `
		SELECT ua.id, ua.account_id, ua.username, ua.first_name, ua.last_name, 
		ua.email, uai.password_hash, uai.password_recovery_token, uai.password_recovery_timeout,
		evs.status, us.status, uai.email_validation_token, uai.email_verification_timeout, ua.bio, ua.address 
		FROM USER_ACCOUNT ua
		LEFT JOIN USER_AUTH_INFO uai ON ua.id = uai.user_id
		LEFT JOIN email_validation_status evs ON uai.email_validation_status = evs.id
		LEFT JOIN user_status us ON ua.user_status = us.id
		WHERE ua.` + identifierType + ` = $1;
	`

	if err := uh.db.QueryRow(query, identifierValue).
		Scan(&tmu.Id, &tmu.AccountId, &tmu.Username, &tmu.FirstName, &tmu.LastName, &tmu.Email,
			&tmu.Password, &tmu.PasswordVerificationToken, &tmu.PasswordVerificationTimeout,
			&tmu.EmailVerificationStatus, &tmu.UserStatus, &tmu.EmailVerificationToken,
			&tmu.EmailVerificationTimeout, &tmu.Bio, &tmu.Location); err != nil {
		uh.log.Errorf("Can't find a user with %s: %s, error: %+v", identifierType, identifierValue, err)
		return nil, err
	}

	return &tmu, nil
}

func (uh *uDBHandler) AddUserInterest(interests []string, username string) error {
	// Start a transaction
	tx, err := uh.db.Begin()
	if err != nil {
		uh.log.Errorf("Failed to start transaction: %+v", err)
		return err
	}

	// Step 1: Fetch the user ID based on username
	var userId int64
	err = tx.QueryRow(`SELECT id FROM user_account WHERE username = $1`, username).Scan(&userId)
	if err != nil {
		uh.log.Errorf("Failed to fetch user ID for username: %s, error: %+v", username, err)
		if err := tx.Rollback(); err != nil {
			uh.log.Errorf("Failed to rollback transaction after error: %+v", err)
		}
		return err
	}

	// Step 2: Iterate over the interests and insert them into the user_interest table
	for _, interest := range interests {
		// Fetch the topic ID based on the interest description
		var topicId int
		err = tx.QueryRow(`SELECT id FROM topics WHERE description = $1`, interest).Scan(&topicId)
		if err != nil {
			uh.log.Errorf("Failed to fetch topic ID for interest: %s, error: %+v", interest, err)
			if err := tx.Rollback(); err != nil {
				uh.log.Errorf("Failed to rollback transaction after error: %+v", err)
			}
			return err
		}

		// Step 3: Check if the user is already following this interest
		var exists int
		err = tx.QueryRow(`SELECT COUNT(1) FROM user_interest WHERE user_id = $1 AND topics_id = $2`, userId, topicId).Scan(&exists)
		if err != nil {
			uh.log.Errorf("Failed to check if user is already following interest: %s, error: %+v", interest, err)
			if err := tx.Rollback(); err != nil {
				uh.log.Errorf("Failed to rollback transaction after error: %+v", err)
			}
			return err
		}

		// If the user is already following the interest, skip the insert and log it
		if exists > 0 {
			uh.log.Debugf("User %s already follows interest: %s, skipping", username, interest)
			continue
		}

		// Insert into user_interest table for interests not already followed
		_, err = tx.Exec(`INSERT INTO user_interest (user_id, topics_id) VALUES ($1, $2)`, userId, topicId)
		if err != nil {
			uh.log.Errorf("Failed to insert user interest for username: %s and interest: %s, error: %+v", username, interest, err)
			if err := tx.Rollback(); err != nil {
				uh.log.Errorf("Failed to rollback transaction after error: %+v", err)
			}
			return err
		}
	}

	// Step 4: Commit the transaction
	if err := tx.Commit(); err != nil {
		uh.log.Errorf("Failed to commit transaction: %+v", err)
		return err
	}

	uh.log.Debugf("Successfully added new interests for user: %s", username)
	return nil
}

func (uh *uDBHandler) GetUserInterest(username string) ([]string, error) {
	// Step 1: Fetch the user ID based on username
	var userId int64
	err := uh.db.QueryRow(`SELECT id FROM user_account WHERE username = $1`, username).Scan(&userId)
	if err != nil {
		uh.log.Errorf("Failed to fetch user ID for username: %s, error: %+v", username, err)
		return nil, err
	}

	// Step 2: Fetch the user's interests (topics) based on user_id
	rows, err := uh.db.Query(`
        SELECT t.description 
        FROM topics t
        JOIN user_interest ui ON t.id = ui.topics_id
        WHERE ui.user_id = $1`, userId)
	if err != nil {
		uh.log.Errorf("Failed to fetch user interests for user ID: %d, error: %+v", userId, err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			uh.log.Errorf("Error closing rows for user interests, error: %+v", err)
		}
	}()

	// Step 3: Collect the descriptions of the topics
	var interests []string
	for rows.Next() {
		var description string
		if err := rows.Scan(&description); err != nil {
			uh.log.Errorf("Failed to scan topic description, error: %+v", err)
			return nil, err
		}
		interests = append(interests, description)
	}

	if err := rows.Err(); err != nil {
		uh.log.Errorf("Error iterating over user interests, error: %+v", err)
		return nil, err
	}

	uh.log.Debugf("Successfully fetched interests for user: %s", username)
	return interests, nil
}

func (uh *uDBHandler) RemoveUserInterest(interests []string, username string) error {
	// Start a transaction
	tx, err := uh.db.Begin()
	if err != nil {
		uh.log.Errorf("Failed to start transaction: %+v", err)
		return err
	}

	// Step 1: Fetch the user ID based on username
	var userId int64
	err = tx.QueryRow(`SELECT id FROM user_account WHERE username = $1`, username).Scan(&userId)
	if err != nil {
		uh.log.Errorf("Failed to fetch user ID for username: %s, error: %+v", username, err)
		if err := tx.Rollback(); err != nil {
			uh.log.Errorf("Failed to rollback transaction after error: %+v", err)
		}
		return err
	}

	// Step 2: Iterate over the interests and remove them from the user_interest table
	for _, interest := range interests {
		// Fetch the topic ID based on the interest description
		var topicId int
		err = tx.QueryRow(`SELECT id FROM topics WHERE description = $1`, interest).Scan(&topicId)
		if err != nil {
			uh.log.Errorf("Failed to fetch topic ID for interest: %s, error: %+v", interest, err)
			if err := tx.Rollback(); err != nil {
				uh.log.Errorf("Failed to rollback transaction after error: %+v", err)
			}
			return err
		}

		// Step 3: Check if the user is actually following this interest
		var exists int
		err = tx.QueryRow(`SELECT COUNT(1) FROM user_interest WHERE user_id = $1 AND topics_id = $2`, userId, topicId).Scan(&exists)
		if err != nil {
			uh.log.Errorf("Failed to check if user follows interest: %s, error: %+v", interest, err)
			if err := tx.Rollback(); err != nil {
				uh.log.Errorf("Failed to rollback transaction after error: %+v", err)
			}
			return err
		}

		// If the user is not following the interest, skip and log it
		if exists == 0 {
			uh.log.Debugf("User %s does not follow interest: %s, skipping removal", username, interest)
			continue
		}

		// Remove the user's interest from the user_interest table
		_, err = tx.Exec(`DELETE FROM user_interest WHERE user_id = $1 AND topics_id = $2`, userId, topicId)
		if err != nil {
			uh.log.Errorf("Failed to remove user interest for username: %s and interest: %s, error: %+v", username, interest, err)
			if err := tx.Rollback(); err != nil {
				uh.log.Errorf("Failed to rollback transaction after error: %+v", err)
			}
			return err
		}
	}

	// Step 4: Commit the transaction
	if err := tx.Commit(); err != nil {
		uh.log.Errorf("Failed to commit transaction: %+v", err)
		return err
	}

	uh.log.Debugf("Successfully removed selected interests for user: %s", username)
	return nil
}

func (uh *uDBHandler) FollowAUser(followingUsername, followersUsername string) error {
	tx, err := uh.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			uh.log.Errorf("Failed to rollback transaction for following user %s by user %s, error: %+v", followingUsername, followersUsername, err)
		}
	}()

	var followingID, followersID int64

	// Step 1: Fetch the user IDs using the usernames
	if err := tx.QueryRow(`SELECT id FROM user_account WHERE username = $1`, followingUsername).Scan(&followingID); err != nil {
		uh.log.Errorf("Can't get ID for username %s, error: %+v", followingUsername, err)
		return err
	}

	if err := tx.QueryRow(`SELECT id FROM user_account WHERE username = $1`, followersUsername).Scan(&followersID); err != nil {
		uh.log.Errorf("Can't get ID for username %s, error: %+v", followersUsername, err)
		return err
	}

	// Step 2: Insert follow relationship
	_, err = tx.Exec(`INSERT INTO user_follows (follower_id, following_id, created_at) VALUES ($1, $2, CURRENT_TIMESTAMP) ON CONFLICT DO NOTHING`, followersID, followingID)
	if err != nil {
		uh.log.Errorf("Failed to insert follow relationship between follower ID %d and following ID %d, error: %+v", followersID, followingID, err)
		return err
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		uh.log.Errorf("Failed to commit transaction for following user %s by user %s, error: %+v", followingUsername, followersUsername, err)
		return err
	}

	uh.log.Debugf("Successfully followed user: %s by user: %s", followingUsername, followersUsername)
	return nil
}

func (uh *uDBHandler) UnFollowAUser(followingUsername, followersUsername string) error {
	tx, err := uh.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			uh.log.Errorf("Failed to rollback transaction for unfollowing user %s by user %s, error: %+v", followingUsername, followersUsername, err)
		}
	}()

	var followingID, followersID int64

	// Step 1: Fetch the user IDs using the usernames
	if err := tx.QueryRow(`SELECT id FROM user_account WHERE username = $1`, followingUsername).Scan(&followingID); err != nil {
		uh.log.Errorf("Can't get ID for username %s, error: %+v", followingUsername, err)
		return err
	}

	if err := tx.QueryRow(`SELECT id FROM user_account WHERE username = $1`, followersUsername).Scan(&followersID); err != nil {
		uh.log.Errorf("Can't get ID for username %s, error: %+v", followersUsername, err)
		return err
	}

	// Step 2: Delete follow relationship
	_, err = tx.Exec(`DELETE FROM user_follows WHERE follower_id = $1 AND following_id = $2`, followersID, followingID)
	if err != nil {
		uh.log.Errorf("Failed to delete follow relationship between follower ID %d and following ID %d, error: %+v", followersID, followingID, err)
		return err
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		uh.log.Errorf("Failed to commit transaction for unfollowing user %s by user %s, error: %+v", followingUsername, followersUsername, err)
		return err
	}

	uh.log.Debugf("Successfully unfollowed user: %s by user: %s", followingUsername, followersUsername)
	return nil
}

func (uh *uDBHandler) GetFollowings(username string) ([]models.TheMonkeysUser, error) {
	var users []models.TheMonkeysUser

	// Step 1: Fetch the user ID using the username
	var userID int64
	if err := uh.db.QueryRow(`SELECT id FROM user_account WHERE username = $1`, username).Scan(&userID); err != nil {
		uh.log.Errorf("Can't get ID for username %s, error: %+v", username, err)
		return nil, err
	}

	// Step 2: Fetch the list of users followed by the given user
	rows, err := uh.db.Query(`
		SELECT ua.username, ua.first_name, ua.last_name, ua.account_id
		FROM user_follows uf
		JOIN user_account ua ON uf.following_id = ua.id
		WHERE uf.follower_id = $1
	`, userID)
	if err != nil {
		uh.log.Errorf("Failed to fetch users followed by user ID %d, error: %+v", userID, err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			uh.log.Errorf("Error closing rows for user ID %d, error: %+v", userID, err)
		}
	}()

	// Step 3: Iterate through the result set and populate the list of users
	for rows.Next() {
		var user models.TheMonkeysUser
		if err := rows.Scan(&user.Username, &user.FirstName, &user.LastName, &user.AccountId); err != nil {
			uh.log.Errorf("Failed to scan user followed by user ID %d, error: %+v", userID, err)
			return nil, err
		}
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		uh.log.Errorf("Error occurred while iterating users followed by user ID %d, error: %+v", userID, err)
		return nil, err
	}

	uh.log.Debugf("Successfully fetched users followed by user: %s", username)
	return users, nil
}

func (uh *uDBHandler) GetFollowers(username string) ([]models.TheMonkeysUser, error) {
	var users []models.TheMonkeysUser

	// Step 1: Fetch the user ID using the username
	var userID int64
	if err := uh.db.QueryRow(`SELECT id FROM user_account WHERE username = $1`, username).Scan(&userID); err != nil {
		uh.log.Errorf("Can't get ID for username %s, error: %+v", username, err)
		return nil, err
	}

	// Step 2: Fetch the list of users who follow the given user
	rows, err := uh.db.Query(`
		SELECT ua.username, ua.first_name, ua.last_name
		FROM user_follows uf
		JOIN user_account ua ON uf.follower_id = ua.id
		WHERE uf.following_id = $1
	`, userID)
	if err != nil {
		uh.log.Errorf("Failed to fetch users who follow user ID %d, error: %+v", userID, err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			uh.log.Errorf("Error closing rows for user ID %d, error: %+v", userID, err)
		}
	}()

	// Step 3: Iterate through the result set and populate the list of users
	for rows.Next() {
		var user models.TheMonkeysUser
		if err := rows.Scan(&user.Username, &user.FirstName, &user.LastName); err != nil {
			uh.log.Errorf("Failed to scan user who follows user ID %d, error: %+v", userID, err)
			return nil, err
		}
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		uh.log.Errorf("Error occurred while iterating users who follow user ID %d, error: %+v", userID, err)
		return nil, err
	}

	uh.log.Debugf("Successfully fetched users who follow user: %s", username)
	return users, nil
}

func (uh *uDBHandler) GetUsersByAccountIds(accIds []string) ([]models.UserProfileRes, error) {
	var users []models.UserProfileRes
	query := `
		SELECT ua.username, ua.first_name, ua.last_name, 
		ua.bio, ua.avatar_url, ua.address
		FROM user_account ua
		WHERE ua.account_id = ANY($1);
	`
	rows, err := uh.db.Query(query, accIds)
	if err != nil {
		uh.log.Errorf("Error fetching batch users: %v", err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var u models.UserProfileRes
		if err := rows.Scan(&u.Username, &u.FirstName, &u.LastName,
			&u.Bio, &u.AvatarUrl, &u.Address); err != nil {
			return nil, err
		}
		users = append(users, u)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}
