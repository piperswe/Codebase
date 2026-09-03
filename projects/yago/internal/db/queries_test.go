package db

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"codebase.bid/lib/go/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

func newTestQueries(t *testing.T, fixture ...string) *Queries {
	t.Helper()
	testDatabase := testdb.New(t, &Migrator)
	conn := testDatabase.GetConn()
	for _, statement := range fixture {
		if _, err := conn.Exec(t.Context(), statement); err != nil {
			t.Fatalf("applying fixture: %v", err)
		}
	}
	return New(conn)
}

func mustQuery[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func requireEqual[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func requirePostEqual(t *testing.T, got, want Post) {
	t.Helper()
	if got.PublishedAt.Valid != want.PublishedAt.Valid ||
		(got.PublishedAt.Valid && !got.PublishedAt.Time.Equal(want.PublishedAt.Time)) {
		t.Errorf("PublishedAt = %+v, want %+v", got.PublishedAt, want.PublishedAt)
	}
	got.PublishedAt = want.PublishedAt
	requireEqual(t, got, want)
}

func testTimestamp(hour int) pgtype.Timestamptz {
	return pgtype.Timestamptz{
		Time:  time.Date(2026, time.September, 3, hour, 0, 0, 0, time.UTC),
		Valid: true,
	}
}

func TestUserAndProfileQueries(t *testing.T) {
	queries := newTestQueries(t)

	requireEqual(t, mustQuery(queries.GetUserCount(t.Context())), int64(0))

	user := mustQuery(queries.CreateUserWithUlid(t.Context(), CreateUserWithUlidParams{
		Ulid:         "user-alice",
		EmailAddress: "alice@example.test",
		PasswordHash: "hash-1",
	}))
	requireEqual(t, user, User{ID: 1, Ulid: "user-alice", EmailAddress: "alice@example.test", PasswordHash: "hash-1"})
	otherUser := mustQuery(queries.CreateUserWithUlid(t.Context(), CreateUserWithUlidParams{
		Ulid:         "user-bob",
		EmailAddress: "bob@example.test",
		PasswordHash: "hash-2",
	}))
	requireEqual(t, otherUser, User{ID: 2, Ulid: "user-bob", EmailAddress: "bob@example.test", PasswordHash: "hash-2"})
	requireEqual(t, mustQuery(queries.GetUserCount(t.Context())), int64(2))
	requireEqual(t, mustQuery(queries.GetUser(t.Context(), user.ID)), user)

	updatedUser := mustQuery(queries.UpdateUser(t.Context(), UpdateUserParams{
		UserID:       user.ID,
		EmailAddress: "alice-updated@example.test",
		PasswordHash: "hash-updated",
	}))
	requireEqual(t, updatedUser, User{ID: user.ID, Ulid: user.Ulid, EmailAddress: "alice-updated@example.test", PasswordHash: "hash-updated"})
	requireEqual(t, mustQuery(queries.GetUser(t.Context(), user.ID)), updatedUser)

	profile := mustQuery(queries.CreateProfileWithUlid(t.Context(), CreateProfileWithUlidParams{
		Ulid:        "profile-alice",
		OwnerID:     user.ID,
		Username:    "alice",
		DisplayName: pgtype.Text{String: "Alice", Valid: true},
	}))
	requireEqual(t, profile, Profile{
		ID:          1,
		Ulid:        "profile-alice",
		OwnerID:     user.ID,
		Username:    "alice",
		DisplayName: pgtype.Text{String: "Alice", Valid: true},
	})
	secondProfile := mustQuery(queries.CreateProfileWithUlid(t.Context(), CreateProfileWithUlidParams{
		Ulid:     "profile-alice-work",
		OwnerID:  user.ID,
		Username: "alice-work",
	}))
	otherProfile := mustQuery(queries.CreateProfileWithUlid(t.Context(), CreateProfileWithUlidParams{
		Ulid:     "profile-bob",
		OwnerID:  otherUser.ID,
		Username: "bob",
	}))
	requireEqual(t, mustQuery(queries.GetProfile(t.Context(), profile.ID)), profile)

	updatedProfile := mustQuery(queries.UpdateProfile(t.Context(), UpdateProfileParams{
		ProfileID: profile.ID,
		Username:  "alice-updated",
	}))
	requireEqual(t, updatedProfile, Profile{
		ID:       profile.ID,
		Ulid:     profile.Ulid,
		OwnerID:  profile.OwnerID,
		Username: "alice-updated",
	})
	requireEqual(t, mustQuery(queries.GetProfile(t.Context(), profile.ID)), updatedProfile)

	profiles := mustQuery(queries.GetUserProfiles(t.Context(), user.ID))
	slices.SortFunc(profiles, func(a, b Profile) int { return int(a.ID - b.ID) })
	requireEqual(t, profiles, []Profile{updatedProfile, secondProfile})
	requireEqual(t, mustQuery(queries.GetUserProfiles(t.Context(), otherUser.ID)), []Profile{otherProfile})
	requireEqual(t, mustQuery(queries.GetUserProfiles(t.Context(), 999)), []Profile(nil))
}

func TestFriendshipQueries(t *testing.T) {
	queries := newTestQueries(t,
		`INSERT INTO users (id, ulid, email_address, password_hash) VALUES
			(1, 'user-1', 'one@example.test', 'hash'),
			(2, 'user-2', 'two@example.test', 'hash'),
			(3, 'user-3', 'three@example.test', 'hash'),
			(4, 'user-4', 'four@example.test', 'hash'),
			(5, 'user-5', 'five@example.test', 'hash'),
			(6, 'user-6', 'six@example.test', 'hash')`,
		`INSERT INTO profiles (id, ulid, owner_id, username, display_name) VALUES
			(1, 'profile-a', 1, 'a', 'Profile A'),
			(2, 'profile-b', 2, 'b', NULL),
			(3, 'profile-c', 3, 'c', 'Profile C'),
			(4, 'profile-d', 4, 'd', 'Profile D'),
			(5, 'profile-e', 5, 'e', NULL),
			(6, 'profile-f', 6, 'f', 'Profile F')`,
		`INSERT INTO friendships (id, requester_id, addressee_id, status) VALUES
			(10, 3, 1, 'pending'),
			(11, 1, 4, 'accepted'),
			(12, 5, 1, 'accepted'),
			(13, 1, 6, 'rejected')`,
	)

	created := mustQuery(queries.CreateUserFriendRequest(t.Context(), CreateUserFriendRequestParams{
		RequesterID: 1,
		AddresseeID: 2,
	}))
	requireEqual(t, created, Friendship{ID: 1, RequesterID: 1, AddresseeID: 2, Status: FriendshipStatusPending})

	outgoing := mustQuery(queries.GetUserOutgoingFriendshipRequests(t.Context(), 1))
	requireEqual(t, outgoing, []GetUserOutgoingFriendshipRequestsRow{{
		ID:               2,
		Ulid:             "profile-b",
		OwnerID:          2,
		Username:         "b",
		FriendshipID:     created.ID,
		FriendshipStatus: FriendshipStatusPending,
	}})
	incoming := mustQuery(queries.GetUserIncomingFriendshipRequests(t.Context(), 2))
	requireEqual(t, incoming, []GetUserIncomingFriendshipRequestsRow{{
		ID:               1,
		Ulid:             "profile-a",
		OwnerID:          1,
		Username:         "a",
		DisplayName:      pgtype.Text{String: "Profile A", Valid: true},
		FriendshipID:     created.ID,
		FriendshipStatus: FriendshipStatusPending,
	}})
	requireEqual(t, mustQuery(queries.GetUserIncomingFriendshipRequests(t.Context(), 1)), []GetUserIncomingFriendshipRequestsRow{{
		ID:               3,
		Ulid:             "profile-c",
		OwnerID:          3,
		Username:         "c",
		DisplayName:      pgtype.Text{String: "Profile C", Valid: true},
		FriendshipID:     10,
		FriendshipStatus: FriendshipStatusPending,
	}})

	friends := mustQuery(queries.GetUserFriends(t.Context(), 1))
	slices.SortFunc(friends, func(a, b GetUserFriendsRow) int { return int(a.ID - b.ID) })
	requireEqual(t, friends, []GetUserFriendsRow{
		{ID: 4, Ulid: "profile-d", OwnerID: 4, Username: "d", DisplayName: pgtype.Text{String: "Profile D", Valid: true}, FriendshipID: 11, FriendshipStatus: FriendshipStatusAccepted},
		{ID: 5, Ulid: "profile-e", OwnerID: 5, Username: "e", FriendshipID: 12, FriendshipStatus: FriendshipStatusAccepted},
	})

	accepted := mustQuery(queries.AcceptUserFriendRequest(t.Context(), created.ID))
	requireEqual(t, accepted, Friendship{ID: created.ID, RequesterID: 1, AddresseeID: 2, Status: FriendshipStatusAccepted})
	requireEqual(t, mustQuery(queries.GetUserOutgoingFriendshipRequests(t.Context(), 1)), []GetUserOutgoingFriendshipRequestsRow(nil))
	requireEqual(t, mustQuery(queries.GetUserIncomingFriendshipRequests(t.Context(), 2)), []GetUserIncomingFriendshipRequestsRow(nil))

	rejected := mustQuery(queries.RejectUserFriendRequest(t.Context(), 10))
	requireEqual(t, rejected, Friendship{ID: 10, RequesterID: 3, AddresseeID: 1, Status: FriendshipStatusRejected})
	requireEqual(t, mustQuery(queries.GetUserIncomingFriendshipRequests(t.Context(), 1)), []GetUserIncomingFriendshipRequestsRow(nil))

	removed := mustQuery(queries.Unfriend(t.Context(), 11))
	requireEqual(t, removed, Friendship{ID: 11, RequesterID: 1, AddresseeID: 4, Status: FriendshipStatusAccepted})
	friends = mustQuery(queries.GetUserFriends(t.Context(), 1))
	slices.SortFunc(friends, func(a, b GetUserFriendsRow) int { return int(a.ID - b.ID) })
	requireEqual(t, friends, []GetUserFriendsRow{
		{ID: 2, Ulid: "profile-b", OwnerID: 2, Username: "b", FriendshipID: 1, FriendshipStatus: FriendshipStatusAccepted},
		{ID: 5, Ulid: "profile-e", OwnerID: 5, Username: "e", FriendshipID: 12, FriendshipStatus: FriendshipStatusAccepted},
	})
}

func TestRoleAndPermissionQueries(t *testing.T) {
	queries := newTestQueries(t,
		`INSERT INTO users (id, ulid, email_address, password_hash) VALUES
			(1, 'user-1', 'one@example.test', 'hash'),
			(2, 'user-2', 'two@example.test', 'hash')`,
		`INSERT INTO roles (id, name) VALUES (1, 'admin'), (2, 'editor'), (3, 'unused')`,
		`INSERT INTO permission_assignments (role_id, permission) VALUES
			(1, 'posts:read'),
			(1, 'posts:write'),
			(2, 'posts:read'),
			(2, 'posts:publish'),
			(3, 'users:delete')`,
		`INSERT INTO role_assignments (user_id, role_id) VALUES (1, 1), (1, 2), (2, 3)`,
	)

	requireEqual(t, mustQuery(queries.GetRole(t.Context(), 1)), Role{ID: 1, Name: "admin"})
	rolePermissions := mustQuery(queries.GetRolePermissions(t.Context(), 1))
	slices.Sort(rolePermissions)
	requireEqual(t, rolePermissions, []string{"posts:read", "posts:write"})
	requireEqual(t, mustQuery(queries.GetRolePermissions(t.Context(), 999)), []string(nil))

	roles := mustQuery(queries.GetUserRoles(t.Context(), 1))
	slices.Sort(roles)
	requireEqual(t, roles, []int64{1, 2})
	requireEqual(t, mustQuery(queries.GetUserRoles(t.Context(), 999)), []int64(nil))

	permissions := mustQuery(queries.GetUserPermissions(t.Context(), 1))
	slices.Sort(permissions)
	requireEqual(t, permissions, []string{"posts:publish", "posts:read", "posts:write"})
	requireEqual(t, mustQuery(queries.GetUserPermissions(t.Context(), 999)), []string(nil))

	requireEqual(t, mustQuery(queries.UserHasPermission(t.Context(), UserHasPermissionParams{UserID: 1, Permission: "posts:write"})), true)
	requireEqual(t, mustQuery(queries.UserHasPermission(t.Context(), UserHasPermissionParams{UserID: 1, Permission: "users:delete"})), false)
	requireEqual(t, mustQuery(queries.UserHasPermission(t.Context(), UserHasPermissionParams{UserID: 999, Permission: "posts:read"})), false)

	requireEqual(t, mustQuery(queries.UserHasOneOfPermissions(t.Context(), UserHasOneOfPermissionsParams{UserID: 1, Permissions: []string{"missing", "posts:publish"}})), true)
	requireEqual(t, mustQuery(queries.UserHasOneOfPermissions(t.Context(), UserHasOneOfPermissionsParams{UserID: 1, Permissions: []string{"missing", "users:delete"}})), false)
	requireEqual(t, mustQuery(queries.UserHasOneOfPermissions(t.Context(), UserHasOneOfPermissionsParams{UserID: 1, Permissions: []string{}})), false)
}

func TestCircleQueries(t *testing.T) {
	queries := newTestQueries(t,
		`INSERT INTO users (id, ulid, email_address, password_hash) VALUES
			(1, 'user-1', 'one@example.test', 'hash'),
			(2, 'user-2', 'two@example.test', 'hash'),
			(3, 'user-3', 'three@example.test', 'hash')`,
		`INSERT INTO profiles (id, ulid, owner_id, username, display_name) VALUES
			(1, 'profile-1', 1, 'one', 'One'),
			(2, 'profile-2', 2, 'two', NULL),
			(3, 'profile-3', 3, 'three', 'Three')`,
		`INSERT INTO circles (id, ulid, user_id, name) VALUES
			(1, 'circle-friends', 1, 'friends'),
			(2, 'circle-family', 1, 'family'),
			(3, 'circle-other', 2, 'other')`,
	)

	circles := mustQuery(queries.GetUserCircles(t.Context(), 1))
	slices.SortFunc(circles, func(a, b Circle) int { return int(a.ID - b.ID) })
	requireEqual(t, circles, []Circle{
		{ID: 1, Ulid: "circle-friends", UserID: 1, Name: "friends"},
		{ID: 2, Ulid: "circle-family", UserID: 1, Name: "family"},
	})
	requireEqual(t, mustQuery(queries.GetUserCircles(t.Context(), 999)), []Circle(nil))

	firstMembership := mustQuery(queries.AddProfileToCircle(t.Context(), AddProfileToCircleParams{CircleID: 1, ProfileID: 1}))
	secondMembership := mustQuery(queries.AddProfileToCircle(t.Context(), AddProfileToCircleParams{CircleID: 1, ProfileID: 2}))
	otherMembership := mustQuery(queries.AddProfileToCircle(t.Context(), AddProfileToCircleParams{CircleID: 2, ProfileID: 3}))
	requireEqual(t, firstMembership, CircleMembership{ID: 1, CircleID: 1, ProfileID: 1})
	requireEqual(t, secondMembership, CircleMembership{ID: 2, CircleID: 1, ProfileID: 2})
	requireEqual(t, otherMembership, CircleMembership{ID: 3, CircleID: 2, ProfileID: 3})

	profiles := mustQuery(queries.GetProfilesInCircle(t.Context(), 1))
	slices.SortFunc(profiles, func(a, b GetProfilesInCircleRow) int { return int(a.ID - b.ID) })
	requireEqual(t, profiles, []GetProfilesInCircleRow{
		{ID: 1, Ulid: "profile-1", OwnerID: 1, Username: "one", DisplayName: pgtype.Text{String: "One", Valid: true}, CircleMembershipID: firstMembership.ID},
		{ID: 2, Ulid: "profile-2", OwnerID: 2, Username: "two", CircleMembershipID: secondMembership.ID},
	})
	requireEqual(t, mustQuery(queries.GetProfilesInCircle(t.Context(), 999)), []GetProfilesInCircleRow(nil))

	removed := mustQuery(queries.RemoveProfileFromCircle(t.Context(), firstMembership.ID))
	requireEqual(t, removed, firstMembership)
	requireEqual(t, mustQuery(queries.GetProfilesInCircle(t.Context(), 1)), []GetProfilesInCircleRow{{
		ID:                 2,
		Ulid:               "profile-2",
		OwnerID:            2,
		Username:           "two",
		CircleMembershipID: secondMembership.ID,
	}})
}

func TestPostQueries(t *testing.T) {
	queries := newTestQueries(t,
		`INSERT INTO users (id, ulid, email_address, password_hash) VALUES
			(1, 'user-author', 'author@example.test', 'hash'),
			(2, 'user-other', 'other@example.test', 'hash')`,
		`INSERT INTO profiles (id, ulid, owner_id, username) VALUES
			(1, 'profile-author-main', 1, 'author-main'),
			(2, 'profile-author-alt', 1, 'author-alt'),
			(3, 'profile-other', 2, 'other')`,
		`INSERT INTO circles (id, ulid, user_id, name) VALUES
			(1, 'circle-1', 1, 'one'),
			(2, 'circle-2', 1, 'two'),
			(3, 'circle-3', 2, 'three')`,
	)

	firstPost := mustQuery(queries.CreatePost(t.Context(), CreatePostParams{
		UserID:      1,
		Content:     "first post",
		PublishedAt: testTimestamp(10),
	}))
	requirePostEqual(t, firstPost, Post{ID: 1, UserID: 1, Content: "first post", PublishedAt: testTimestamp(10)})
	secondPost := mustQuery(queries.CreatePost(t.Context(), CreatePostParams{UserID: 1, Content: "draft"}))
	requirePostEqual(t, secondPost, Post{ID: 2, UserID: 1, Content: "draft"})
	otherPost := mustQuery(queries.CreatePost(t.Context(), CreatePostParams{UserID: 2, Content: "other user", PublishedAt: testTimestamp(12)}))
	requirePostEqual(t, mustQuery(queries.GetPost(t.Context(), firstPost.ID)), firstPost)

	secondPost = mustQuery(queries.UpdatePost(t.Context(), UpdatePostParams{
		PostID:      secondPost.ID,
		Content:     "published draft",
		PublishedAt: testTimestamp(11),
	}))
	requirePostEqual(t, secondPost, Post{ID: 2, UserID: 1, Content: "published draft", PublishedAt: testTimestamp(11)})
	requirePostEqual(t, mustQuery(queries.GetPost(t.Context(), secondPost.ID)), secondPost)

	userPosts := mustQuery(queries.GetUserPosts(t.Context(), 1))
	slices.SortFunc(userPosts, func(a, b Post) int { return int(a.ID - b.ID) })
	if len(userPosts) != 2 {
		t.Fatalf("GetUserPosts returned %d rows, want 2: %+v", len(userPosts), userPosts)
	}
	requirePostEqual(t, userPosts[0], firstPost)
	requirePostEqual(t, userPosts[1], secondPost)
	requireEqual(t, mustQuery(queries.GetUserPosts(t.Context(), 999)), []Post(nil))

	firstProfilePost := mustQuery(queries.SharePostOnProfileWithUlid(t.Context(), SharePostOnProfileWithUlidParams{
		Ulid: "profile-post-first-main", ProfileID: 1, PostID: firstPost.ID, Visibility: PostVisibilityPublic,
	}))
	secondProfilePost := mustQuery(queries.SharePostOnProfileWithUlid(t.Context(), SharePostOnProfileWithUlidParams{
		Ulid: "profile-post-first-alt", ProfileID: 2, PostID: firstPost.ID, Visibility: PostVisibilityPrivate,
	}))
	thirdProfilePost := mustQuery(queries.SharePostOnProfileWithUlid(t.Context(), SharePostOnProfileWithUlidParams{
		Ulid: "profile-post-second-main", ProfileID: 1, PostID: secondPost.ID, Visibility: PostVisibilityPrivate,
	}))
	otherProfilePost := mustQuery(queries.SharePostOnProfileWithUlid(t.Context(), SharePostOnProfileWithUlidParams{
		Ulid: "profile-post-other", ProfileID: 3, PostID: otherPost.ID, Visibility: PostVisibilityPublic,
	}))
	requireEqual(t, firstProfilePost, ProfilePost{ID: 1, Ulid: "profile-post-first-main", ProfileID: 1, PostID: 1, Visibility: PostVisibilityPublic})
	requireEqual(t, mustQuery(queries.GetProfilePost(t.Context(), firstProfilePost.ID)), firstProfilePost)

	postProfiles := mustQuery(queries.GetPostProfiles(t.Context(), firstPost.ID))
	slices.SortFunc(postProfiles, func(a, b ProfilePost) int { return int(a.ID - b.ID) })
	requireEqual(t, postProfiles, []ProfilePost{firstProfilePost, secondProfilePost})
	requireEqual(t, mustQuery(queries.GetPostProfiles(t.Context(), 999)), []ProfilePost(nil))

	profilePosts := mustQuery(queries.GetProfilePosts(t.Context(), 1))
	slices.SortFunc(profilePosts, func(a, b GetProfilePostsRow) int { return int(a.ID - b.ID) })
	if len(profilePosts) != 2 {
		t.Fatalf("GetProfilePosts returned %d rows, want 2: %+v", len(profilePosts), profilePosts)
	}
	for index, want := range []struct {
		post        Post
		profilePost ProfilePost
	}{{firstPost, firstProfilePost}, {secondPost, thirdProfilePost}} {
		got := profilePosts[index]
		requirePostEqual(t, Post{ID: got.ID, UserID: got.UserID, Content: got.Content, PublishedAt: got.PublishedAt}, want.post)
		requireEqual(t, got.ProfilePostID, want.profilePost.ID)
		requireEqual(t, got.Visibility, want.profilePost.Visibility)
		requireEqual(t, got.Ulid, want.profilePost.Ulid)
	}
	requireEqual(t, mustQuery(queries.GetProfilePosts(t.Context(), 999)), []GetProfilePostsRow(nil))

	firstShare := mustQuery(queries.SharePostToCircle(t.Context(), SharePostToCircleParams{PostID: firstPost.ID, CircleID: 1}))
	secondShare := mustQuery(queries.SharePostToCircle(t.Context(), SharePostToCircleParams{PostID: firstPost.ID, CircleID: 2}))
	otherShare := mustQuery(queries.SharePostToCircle(t.Context(), SharePostToCircleParams{PostID: secondPost.ID, CircleID: 3}))
	requireEqual(t, firstShare, PostShare{ID: 1, PostID: firstPost.ID, CircleID: 1})
	requireEqual(t, secondShare, PostShare{ID: 2, PostID: firstPost.ID, CircleID: 2})
	requireEqual(t, otherShare, PostShare{ID: 3, PostID: secondPost.ID, CircleID: 3})
	shares := mustQuery(queries.GetPostCircleShares(t.Context(), firstPost.ID))
	slices.SortFunc(shares, func(a, b PostShare) int { return int(a.ID - b.ID) })
	requireEqual(t, shares, []PostShare{firstShare, secondShare})
	requireEqual(t, mustQuery(queries.GetPostCircleShares(t.Context(), 999)), []PostShare(nil))

	requireEqual(t, mustQuery(queries.UnsharePostFromCircle(t.Context(), firstShare.ID)), firstShare)
	requireEqual(t, mustQuery(queries.GetPostCircleShares(t.Context(), firstPost.ID)), []PostShare{secondShare})
	requireEqual(t, mustQuery(queries.UnsharePostOnProfile(t.Context(), secondProfilePost.ID)), secondProfilePost)
	requireEqual(t, mustQuery(queries.GetPostProfiles(t.Context(), firstPost.ID)), []ProfilePost{firstProfilePost})

	requirePostEqual(t, mustQuery(queries.DeletePost(t.Context(), secondPost.ID)), secondPost)
	if _, err := queries.GetPost(t.Context(), secondPost.ID); err == nil {
		t.Error("GetPost succeeded after DeletePost")
	}
	userPosts = mustQuery(queries.GetUserPosts(t.Context(), 1))
	if len(userPosts) != 1 {
		t.Fatalf("GetUserPosts returned %d rows after DeletePost, want 1: %+v", len(userPosts), userPosts)
	}
	requirePostEqual(t, userPosts[0], firstPost)
	if _, err := queries.GetProfilePost(t.Context(), thirdProfilePost.ID); err == nil {
		t.Error("GetProfilePost succeeded for a row cascaded by DeletePost")
	}
	requireEqual(t, otherProfilePost.PostID, otherPost.ID)
}

func TestGetProfilePostsVisibleToUser(t *testing.T) {
	testDatabase := testdb.New(t, &Migrator)
	conn := testDatabase.GetConn()

	fixture := []string{
		`INSERT INTO users (id, ulid, email_address, password_hash) VALUES
			(1, 'user-author', 'author@example.test', 'hash'),
			(2, 'user-viewer', 'viewer@example.test', 'hash'),
			(3, 'user-stranger', 'stranger@example.test', 'hash'),
			(4, 'user-without-profile', 'without-profile@example.test', 'hash')`,
		`INSERT INTO profiles (id, ulid, owner_id, username) VALUES
			(1, 'profile-target', 1, 'target'),
			(2, 'profile-viewer-primary', 2, 'viewer-primary'),
			(3, 'profile-viewer-secondary', 2, 'viewer-secondary'),
			(4, 'profile-stranger', 3, 'stranger'),
			(5, 'profile-other-target', 1, 'other-target'),
			(6, 'profile-without-posts', 1, 'without-posts')`,
		`INSERT INTO circles (id, ulid, user_id, name) VALUES
			(1, 'circle-viewer-shared', 1, 'viewer shared'),
			(2, 'circle-secondary-profile', 1, 'secondary profile'),
			(3, 'circle-stranger', 1, 'stranger')`,
		`INSERT INTO circle_memberships (id, circle_id, profile_id) VALUES
			(1, 1, 2),
			(2, 1, 3),
			(3, 2, 3),
			(4, 3, 4)`,
		`INSERT INTO posts (id, user_id, content, published_at) VALUES
			(1, 1, 'public unshared', '2026-09-03 02:00:00+00'),
			(2, 1, 'private viewer circle', '2026-09-03 01:00:00+00'),
			(3, 1, 'private unshared', '2026-09-03 03:00:00+00'),
			(4, 1, 'public viewer circle', '2026-09-03 04:00:00+00'),
			(5, 1, 'private multiple viewer circles', '2026-09-03 05:00:00+00'),
			(6, 1, 'private stranger circle', '2026-09-03 06:00:00+00'),
			(7, 1, 'private secondary profile circle', '2026-09-03 07:00:00+00'),
			(8, 1, 'public other profile', '2026-09-03 09:00:00+00'),
			(9, 1, 'private other profile viewer circle', '2026-09-03 08:00:00+00'),
			(10, 1, 'unpublished public post', NULL)`,
		`INSERT INTO profile_posts (id, ulid, profile_id, post_id, visibility) VALUES
			(101, 'profile-post-public-unshared', 1, 1, 'public'),
			(102, 'profile-post-private-viewer-circle', 1, 2, 'private'),
			(103, 'profile-post-private-unshared', 1, 3, 'private'),
			(104, 'profile-post-public-viewer-circle', 1, 4, 'public'),
			(105, 'profile-post-private-multiple-viewer-circles', 1, 5, 'private'),
			(106, 'profile-post-private-stranger-circle', 1, 6, 'private'),
			(107, 'profile-post-private-secondary-profile-circle', 1, 7, 'private'),
			(108, 'profile-post-public-other-profile', 5, 8, 'public'),
			(109, 'profile-post-private-other-profile-viewer-circle', 5, 9, 'private'),
			(110, 'profile-post-unpublished-public-post', 1, 10, 'public')`,
		`INSERT INTO post_shares (id, post_id, circle_id) VALUES
			(1, 2, 1),
			(2, 4, 1),
			(3, 5, 1),
			(4, 5, 2),
			(5, 6, 3),
			(6, 7, 2),
			(7, 9, 1)`,
	}
	for _, statement := range fixture {
		if _, err := conn.Exec(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}

	queries := New(conn)
	publishedAt := func(hour int) pgtype.Timestamptz {
		return pgtype.Timestamptz{
			Time:  time.Date(2026, time.September, 3, hour, 0, 0, 0, time.UTC),
			Valid: true,
		}
	}
	allRows := map[int64]GetProfilePostsVisibleToUserRow{
		1: {ID: 1, UserID: 1, Content: "public unshared", PublishedAt: publishedAt(2), ProfilePostID: 101, Visibility: PostVisibilityPublic, Ulid: "profile-post-public-unshared"},
		2: {ID: 2, UserID: 1, Content: "private viewer circle", PublishedAt: publishedAt(1), ProfilePostID: 102, Visibility: PostVisibilityPrivate, Ulid: "profile-post-private-viewer-circle"},
		3: {ID: 3, UserID: 1, Content: "private unshared", PublishedAt: publishedAt(3), ProfilePostID: 103, Visibility: PostVisibilityPrivate, Ulid: "profile-post-private-unshared"},
		4: {ID: 4, UserID: 1, Content: "public viewer circle", PublishedAt: publishedAt(4), ProfilePostID: 104, Visibility: PostVisibilityPublic, Ulid: "profile-post-public-viewer-circle"},
		5: {ID: 5, UserID: 1, Content: "private multiple viewer circles", PublishedAt: publishedAt(5), ProfilePostID: 105, Visibility: PostVisibilityPrivate, Ulid: "profile-post-private-multiple-viewer-circles"},
		6: {ID: 6, UserID: 1, Content: "private stranger circle", PublishedAt: publishedAt(6), ProfilePostID: 106, Visibility: PostVisibilityPrivate, Ulid: "profile-post-private-stranger-circle"},
		7: {ID: 7, UserID: 1, Content: "private secondary profile circle", PublishedAt: publishedAt(7), ProfilePostID: 107, Visibility: PostVisibilityPrivate, Ulid: "profile-post-private-secondary-profile-circle"},
		8: {ID: 8, UserID: 1, Content: "public other profile", PublishedAt: publishedAt(9), ProfilePostID: 108, Visibility: PostVisibilityPublic, Ulid: "profile-post-public-other-profile"},
		9: {ID: 9, UserID: 1, Content: "private other profile viewer circle", PublishedAt: publishedAt(8), ProfilePostID: 109, Visibility: PostVisibilityPrivate, Ulid: "profile-post-private-other-profile-viewer-circle"},
	}

	tests := []struct {
		name            string
		userID          int64
		profileID       int64
		publishedBefore time.Time
		pageSize        int32
		wantIDs         []int64
	}{
		{
			name:      "viewer sees public and shared private posts without duplicates",
			userID:    2,
			profileID: 1,
			pageSize:  100,
			wantIDs:   []int64{2, 1, 4, 5, 7},
		},
		{
			name:      "another user sees public posts and posts shared with their circle",
			userID:    3,
			profileID: 1,
			pageSize:  100,
			wantIDs:   []int64{1, 4, 6},
		},
		{
			name:      "user without profiles sees only public posts",
			userID:    4,
			profileID: 1,
			pageSize:  100,
			wantIDs:   []int64{1, 4},
		},
		{
			name:      "profile owner does not bypass private visibility",
			userID:    1,
			profileID: 1,
			pageSize:  100,
			wantIDs:   []int64{1, 4},
		},
		{
			name:      "results are limited to the requested profile",
			userID:    2,
			profileID: 5,
			pageSize:  100,
			wantIDs:   []int64{9, 8},
		},
		{
			name:            "publication timestamp excludes later and equal posts",
			userID:          2,
			profileID:       1,
			publishedBefore: publishedAt(4).Time,
			pageSize:        100,
			wantIDs:         []int64{2, 1},
		},
		{
			name:      "page size limits results",
			userID:    2,
			profileID: 1,
			pageSize:  2,
			wantIDs:   []int64{2, 1},
		},
		{
			name:      "profile without posts returns no posts",
			userID:    2,
			profileID: 6,
			pageSize:  100,
		},
		{
			name:      "unknown profile returns no posts",
			userID:    2,
			profileID: 999,
			pageSize:  100,
		},
	}
	afterAllPosts := time.Date(2026, time.September, 4, 0, 0, 0, 0, time.UTC)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publishedBefore := test.publishedBefore
			if publishedBefore.IsZero() {
				publishedBefore = afterAllPosts
			}
			rows, err := queries.GetProfilePostsVisibleToUser(t.Context(), GetProfilePostsVisibleToUserParams{
				ProfileID: test.profileID,
				PublishedBefore: pgtype.Timestamptz{
					Time:  publishedBefore,
					Valid: true,
				},
				PageSize: test.pageSize,
				UserID:   test.userID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != len(test.wantIDs) {
				t.Fatalf("got %d rows, want %d: %+v", len(rows), len(test.wantIDs), rows)
			}

			for index, wantID := range test.wantIDs {
				want := allRows[wantID]
				got := rows[index]
				if got.PublishedAt.Valid != want.PublishedAt.Valid || !got.PublishedAt.Time.Equal(want.PublishedAt.Time) {
					t.Errorf("rows[%d].PublishedAt = %+v, want %+v", index, got.PublishedAt, want.PublishedAt)
				}
				got.PublishedAt = want.PublishedAt
				if got != want {
					t.Errorf("rows[%d] = %+v, want %+v", index, got, want)
				}
			}
		})
	}
}
