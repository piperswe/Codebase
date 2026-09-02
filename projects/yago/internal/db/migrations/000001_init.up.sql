CREATE TABLE users (
    id INTEGER PRIMARY KEY NOT NULL,
    email_address TEXT NOT NULL,
    password_hash TEXT NOT NULL
);

CREATE INDEX users_by_email_address ON users (email_address);

CREATE TABLE roles (
    id INTEGER PRIMARY KEY NOT NULL,
    name TEXT NOT NULL
);

CREATE TABLE permission_assignments (
    id INTEGER PRIMARY KEY NOT NULL,
    role_id INTEGER NOT NULL,
    permission TEXT NOT NULL,
    FOREIGN KEY (role_id) REFERENCES roles (id)
);

CREATE INDEX permission_assignments_by_role_id ON permission_assignments (role_id);

CREATE TABLE role_assignments (
    id INTEGER PRIMARY KEY NOT NULL,
    user_id INTEGER NOT NULL,
    role_id INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users (id),
    FOREIGN KEY (role_id) REFERENCES roles (id)
);

CREATE INDEX role_assignments_by_user_id ON role_assignments (user_id);
CREATE INDEX role_assignments_by_role_id ON role_assignments (role_id);