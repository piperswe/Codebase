CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    ulid TEXT NOT NULL,
    email_address TEXT NOT NULL,
    password_hash TEXT NOT NULL
);

CREATE UNIQUE INDEX users_by_ulid ON users (ulid);
CREATE INDEX users_by_email_address ON users (email_address);

CREATE TABLE roles (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    name TEXT NOT NULL
);

CREATE TABLE permission_assignments (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    role_id BIGINT NOT NULL,
    permission TEXT NOT NULL,
    FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE
);

CREATE INDEX permission_assignments_by_role_id ON permission_assignments (role_id);

CREATE TABLE role_assignments (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    user_id BIGINT NOT NULL,
    role_id BIGINT NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE
);

CREATE INDEX role_assignments_by_user_id ON role_assignments (user_id);
CREATE INDEX role_assignments_by_role_id ON role_assignments (role_id);

CREATE TABLE profiles (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    ulid TEXT NOT NULL,
    owner_id BIGINT NOT NULL,
    username TEXT NOT NULL,
    display_name TEXT,
    FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX profiles_by_ulid ON profiles (ulid);
CREATE INDEX profiles_by_owner_id ON profiles (owner_id);
CREATE UNIQUE INDEX profiles_by_username ON profiles (username);

CREATE TYPE friendship_status AS ENUM ('pending', 'accepted', 'rejected');

CREATE TABLE friendships (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    requester_id BIGINT NOT NULL,
    addressee_id BIGINT NOT NULL,
    status friendship_status NOT NULL,
    FOREIGN KEY (requester_id) REFERENCES profiles (id) ON DELETE CASCADE,
    FOREIGN KEY (addressee_id) REFERENCES profiles (id) ON DELETE CASCADE
);

CREATE INDEX friendships_by_requester_id ON friendships (requester_id);
CREATE INDEX friendships_by_addressee_id ON friendships (addressee_id);
-- ensure that a friendship between two profiles is unique regardless of the requester and addressee order
CREATE UNIQUE INDEX friendships_by_requester_id_and_addressee_id ON friendships (LEAST(requester_id, addressee_id), GREATEST(requester_id, addressee_id));

CREATE TABLE circles (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    ulid TEXT NOT NULL,
    user_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX circles_by_ulid ON circles (ulid);
CREATE INDEX circles_by_user_id ON circles (user_id);
CREATE UNIQUE INDEX circles_by_user_id_and_name ON circles (user_id, name);

CREATE TABLE circle_memberships (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    circle_id BIGINT NOT NULL,
    profile_id BIGINT NOT NULL,
    FOREIGN KEY (circle_id) REFERENCES circles (id) ON DELETE CASCADE,
    FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX circle_memberships_by_circle_id_and_profile_id ON circle_memberships (circle_id, profile_id);
CREATE INDEX circle_memberships_by_profile_id_and_circle_id ON circle_memberships (profile_id, circle_id);

CREATE TABLE posts (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    user_id BIGINT NOT NULL,
    content TEXT NOT NULL,
    published_at TIMESTAMPTZ,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX posts_by_user_id ON posts (user_id);
CREATE INDEX posts_by_published_at_and_id ON posts (published_at, id) WHERE published_at IS NOT NULL;

-- public: visible on public profile
-- private: only visible to circles it's shared with
CREATE TYPE post_visibility AS ENUM ('public', 'private');

-- links a post to a profile, so that a post can have separate ULIDs for each profile it's
-- shared on
CREATE TABLE profile_posts (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    ulid TEXT NOT NULL,
    profile_id BIGINT NOT NULL,
    post_id BIGINT NOT NULL,
    visibility post_visibility NOT NULL,
    FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE,
    FOREIGN KEY (post_id) REFERENCES posts (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX profile_posts_by_profile_id_and_post_id ON profile_posts (profile_id, post_id);
CREATE UNIQUE INDEX profile_posts_by_ulid ON profile_posts (ulid);

CREATE TABLE post_shares (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    post_id BIGINT NOT NULL,
    circle_id BIGINT NOT NULL,
    FOREIGN KEY (post_id) REFERENCES posts (id) ON DELETE CASCADE,
    FOREIGN KEY (circle_id) REFERENCES circles (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX post_shares_by_post_id_and_circle_id ON post_shares (post_id, circle_id);
