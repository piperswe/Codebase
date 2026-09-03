-- name: GetUserCount :one
SELECT COUNT(*) FROM users;

-- name: GetUser :one
SELECT * FROM users WHERE id = @user_id;

-- name: GetUserFriends :many
SELECT profiles.*, friendships.id AS friendship_id, friendships.status AS friendship_status
FROM profiles
INNER JOIN friendships ON profiles.id = friendships.addressee_id OR profiles.id = friendships.requester_id
WHERE (friendships.requester_id = @profile_id OR friendships.addressee_id = @profile_id) AND profiles.id != @profile_id AND friendships.status = 'accepted';

-- name: GetUserIncomingFriendshipRequests :many
SELECT profiles.*, friendships.id AS friendship_id, friendships.status AS friendship_status
FROM profiles
INNER JOIN friendships ON profiles.id = friendships.requester_id
WHERE friendships.addressee_id = @profile_id AND friendships.status = 'pending';

-- name: GetUserOutgoingFriendshipRequests :many
SELECT profiles.*, friendships.id AS friendship_id, friendships.status AS friendship_status
FROM profiles
INNER JOIN friendships ON profiles.id = friendships.addressee_id
WHERE friendships.requester_id = @profile_id AND friendships.status = 'pending';

-- name: CreateUserWithUlid :one
INSERT INTO users (ulid, email_address, password_hash)
VALUES (@ulid, @email_address, @password_hash)
RETURNING *;

-- name: UpdateUser :one
UPDATE users
SET email_address = @email_address,
    password_hash = @password_hash
WHERE id = @user_id
RETURNING *;

-- name: CreateUserFriendRequest :one
INSERT INTO friendships (requester_id, addressee_id, status)
VALUES (@requester_id, @addressee_id, 'pending')
RETURNING *;

-- name: AcceptUserFriendRequest :one
UPDATE friendships
SET status = 'accepted'
WHERE id = @friendship_id
RETURNING *;

-- name: RejectUserFriendRequest :one
UPDATE friendships
SET status = 'rejected'
WHERE id = @friendship_id
RETURNING *;

-- name: Unfriend :one
DELETE FROM friendships
WHERE id = @friendship_id
RETURNING *;

-- name: GetProfile :one
SELECT * FROM profiles WHERE id = @profile_id;

-- name: CreateProfileWithUlid :one
INSERT INTO profiles (ulid, owner_id, username, display_name)
VALUES (@ulid, @owner_id, @username, @display_name)
RETURNING *;

-- name: UpdateProfile :one
UPDATE profiles
SET username = @username,
    display_name = @display_name
WHERE id = @profile_id
RETURNING *;

-- name: GetRole :one
SELECT * FROM roles WHERE id = @role_id;

-- name: GetRolePermissions :many
SELECT permission FROM permission_assignments WHERE role_id = @role_id;

-- name: GetUserRoles :many
SELECT role_id FROM role_assignments WHERE user_id = @user_id;

-- name: GetUserProfiles :many
SELECT * FROM profiles WHERE owner_id = @user_id;

-- name: GetUserPermissions :many
SELECT DISTINCT permission
FROM permission_assignments
INNER JOIN role_assignments ON permission_assignments.role_id = role_assignments.role_id
WHERE role_assignments.user_id = @user_id;

-- name: UserHasPermission :one
SELECT COUNT(*) > 0
FROM permission_assignments
INNER JOIN role_assignments ON permission_assignments.role_id = role_assignments.role_id
WHERE role_assignments.user_id = @user_id AND permission_assignments.permission = @permission;

-- name: UserHasOneOfPermissions :one
SELECT COUNT(*) > 0
FROM permission_assignments
INNER JOIN role_assignments ON permission_assignments.role_id = role_assignments.role_id
WHERE role_assignments.user_id = @user_id AND permission_assignments.permission = ANY(@permissions::text[]);

-- name: GetUserCircles :many
SELECT circles.*
FROM circles
WHERE circles.user_id = @user_id;

-- name: GetProfilesInCircle :many
SELECT profiles.*, circle_memberships.id AS circle_membership_id
FROM profiles
INNER JOIN circle_memberships ON circle_memberships.profile_id = profiles.id
WHERE circle_memberships.circle_id = @circle_id;

-- name: AddProfileToCircle :one
INSERT INTO circle_memberships (circle_id, profile_id)
VALUES (@circle_id, @profile_id)
RETURNING *;

-- name: RemoveProfileFromCircle :one
DELETE FROM circle_memberships
WHERE id = @circle_membership_id
RETURNING *;

-- name: GetPost :one
SELECT * FROM posts WHERE id = @post_id;

-- name: GetPostProfiles :many
SELECT * FROM profile_posts WHERE post_id = @post_id;

-- name: GetUserPosts :many
SELECT *
FROM posts
WHERE user_id = @user_id;

-- name: GetProfilePost :one
SELECT *
FROM profile_posts
WHERE id = @profile_post_id;

-- name: GetProfilePosts :many
SELECT posts.*, profile_posts.id AS profile_post_id, profile_posts.visibility, profile_posts.ulid
FROM profile_posts
INNER JOIN posts ON posts.id = profile_posts.post_id
WHERE profile_posts.profile_id = @profile_id;

-- name: GetProfilePostsVisibleToUser :many
WITH user_profiles AS (
    SELECT id FROM profiles WHERE owner_id = @user_id
)
SELECT posts.*, profile_posts.id AS profile_post_id, profile_posts.visibility, profile_posts.ulid
FROM profile_posts
INNER JOIN posts ON posts.id = profile_posts.post_id
WHERE profile_posts.profile_id = @profile_id
    AND posts.published_at < @published_before
    AND (
        profile_posts.visibility = 'public'
        OR EXISTS (
            SELECT 1
            FROM post_shares
            INNER JOIN circle_memberships ON circle_memberships.circle_id = post_shares.circle_id
            INNER JOIN user_profiles ON user_profiles.id = circle_memberships.profile_id
            WHERE post_shares.post_id = profile_posts.post_id
        )
    )
ORDER BY posts.published_at, posts.id DESC
LIMIT @page_size;

-- name: CreatePost :one
INSERT INTO posts (user_id, content, published_at)
VALUES (@user_id, @content, @published_at)
RETURNING *;

-- name: DeletePost :one
DELETE FROM posts
WHERE id = @post_id
RETURNING *;

-- name: SharePostOnProfileWithUlid :one
INSERT INTO profile_posts (ulid, profile_id, post_id, visibility)
VALUES (@ulid, @profile_id, @post_id, @visibility)
RETURNING *;

-- name: GetPostCircleShares :many
SELECT * FROM post_shares WHERE post_id = @post_id;

-- name: SharePostToCircle :one
INSERT INTO post_shares (post_id, circle_id)
VALUES (@post_id, @circle_id)
RETURNING *;

-- name: UpdatePost :one
UPDATE posts
SET content = @content,
    published_at = @published_at
WHERE id = @post_id
RETURNING *;

-- name: UnsharePostOnProfile :one
DELETE FROM profile_posts
WHERE id = @profile_post_id
RETURNING *;

-- name: UnsharePostFromCircle :one
DELETE FROM post_shares
WHERE id = @post_share_id
RETURNING *;
