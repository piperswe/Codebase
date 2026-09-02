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
    FOREIGN KEY (role_id) REFERENCES roles (id)
);

CREATE INDEX permission_assignments_by_role_id ON permission_assignments (role_id);

CREATE TABLE role_assignments (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    user_id BIGINT NOT NULL,
    role_id BIGINT NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users (id),
    FOREIGN KEY (role_id) REFERENCES roles (id)
);

CREATE INDEX role_assignments_by_user_id ON role_assignments (user_id);
CREATE INDEX role_assignments_by_role_id ON role_assignments (role_id);

CREATE TABLE profiles (
    id BIGSERIAL PRIMARY KEY NOT NULL,
    ulid TEXT NOT NULL,
    owner_id BIGINT NOT NULL,
    username TEXT NOT NULL,
    display_name TEXT,
    FOREIGN KEY (owner_id) REFERENCES users (id)
);

CREATE UNIQUE INDEX profiles_by_ulid ON profiles (ulid);
CREATE INDEX profiles_by_owner_id ON profiles (owner_id);
CREATE UNIQUE INDEX profiles_by_username ON profiles (username);