# LaunchKit — Database Isolation Architecture

> **Important**: this supplements and corrects ARCHITECTURE.md, spelling out the two-layer database isolation design.

---

## 🎯 Core Design Principle

**Putting the projects of all users in the same database is a very bad design**.

The correct architecture separates two layers:

1. **Layer 1: LaunchKit platform database** (shared, stores metadata)
2. **Layer 2: user project databases** (independent, one per project)

---

## 📊 Full Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                    LaunchKit Platform                           │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  Layer 1: LaunchKit Platform Database (Neon Postgres)     │ │
│  │  ───────────────────────────────────────────────────────  │ │
│  │                                                           │ │
│  │  Purpose: platform admin data (metadata, no app data)     │ │
│  │  Provider: Neon Postgres (one database, shared by teams)  │ │
│  │  Isolation: logical, by team_id                           │ │
│  │                                                           │ │
│  │  Tables:                                                  │ │
│  │  - users (Auth0 user ID, email, name)                    │ │
│  │  - teams (team ID, name, owner_id, balance)              │ │
│  │  - team_members (user_id, team_id, role, projects)       │ │
│  │  - projects (project ID, team_id, name, region,          │ │
│  │               user_database_id, user_database_name)      │ │
│  │  - environments (env ID, project_id, name, status)       │ │
│  │  - deployments (deployment ID, project_id, status)       │ │
│  │  - audit_logs (who did what when)                        │ │
│  │  - billing (invoices, usage, top_ups)                    │ │
│  │  - budgets · backups                                     │ │
│  └───────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                          │
          ┌───────────────┼───────────────┐
          │               │               │
          ▼               ▼               ▼
    ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
    │  Layer 2:   │ │  Layer 2:   │ │  Layer 2:   │
    │  User DB A  │ │  User DB B  │ │  User DB C  │
    │  (Neon)     │ │  (Neon)     │ │  (Neon)     │
    │             │ │             │ │             │
    │  Project A  │ │  Project B  │ │  Project C  │
    │  App data   │ │  App data   │ │  App data   │
    │             │ │             │ │             │
    │  - users    │ │  - users    │ │  - users    │
    │  - orders   │ │  - orders   │ │  - orders   │
    │  - products │ │  - products │ │  - products │
    │  - ...      │ │  - ...      │ │  - ...      │
    │  Isolation: │ │  Isolation: │ │  Isolation: │
    │  physical   │ │  physical   │ │  physical   │
    └─────────────┘ └─────────────┘ └─────────────┘
```

---

## 🔑 Key Differences

### Layer 1: Platform Database (shared)

| Property | Description |
|------|------|
| **Purpose** | Stores LaunchKit platform management data |
| **Contents** | User accounts, teams, project metadata, deployment records, billing |
| **Provider** | Neon Postgres (single database) |
| **Isolation** | Logical isolation by `team_id` (Row-Level Security) |
| **Who can access** | The LaunchKit platform (filtered by team_id) |
| **User contact** | Users do not access it directly; it is managed by LaunchKit |

### Layer 2: User Project Databases (independent)

| Property | Description |
|------|------|
| **Purpose** | Stores the business data of the user's application |
| **Contents** | The user's users, orders, products, etc. (entirely defined by the user) |
| **Provider** | Neon Database (one per project) |
| **Isolation** | Physical isolation (separate Databases) |
| **Who can access** | Only the user's own application |
| **User contact** | The user uses it directly through `DATABASE_URL` |

---

## 💡 Why Design It This Way?

### ❌ Wrong Design: All Data Together

```sql
-- all users' data is in the same database
SELECT * FROM orders;  -- sees the orders of all users!
```

**Problems**:
- Risk of horizontal privilege escalation (a failed team_id filter = disaster)
- Complex backup/restore (must filter by team_id)
- Performance interference (big users slow down small users)
- Compliance is hard (GDPR data deletion needs filtering)
- No independent billing (database usage cannot be computed per project)

---

### ✅ Right Design: Physical Isolation

```sql
-- Xiao Wang's application
DATABASE_URL=postgresql://user:pass@ep-abc123.neon.tech/my-saas-db

-- Xiao Wang's code can only access his own database
SELECT * FROM orders;  -- only Xiao Wang's my-saas orders
```

**Advantages**:
- ✅ **True security isolation**: Xiao Wang's code cannot access Company A's database
- ✅ **Independent backups**: each project has its own backup policy
- ✅ **Independent performance**: Company A's queries do not affect Xiao Wang's performance
- ✅ **Independent billing**: Neon usage is computed per project
- ✅ **Easy migration**: users can export their complete database
- ✅ **Compliance friendly**: GDPR data deletion = dropping the whole database

---

## 🚀 Implementation Flow

### Step 1: The user creates a project

```
User action:
  "Claude, create a new SaaS project for me called my-saas"

LaunchKit executes:
  1. Create the project metadata in Layer 1
     INSERT INTO projects (team_id, name) VALUES ('team_001', 'my-saas')
  
  2. Create a separate Database in Neon
     POST /v2/projects/{neon_project_id}/databases
     { "name": "my-saas-db" }
  
  3. Store the database info
     UPDATE projects 
     SET user_database_id = 'neon_db_abc123',
         user_database_name = 'my-saas-db'
     WHERE id = 'project_xyz'
  
  4. Inject the connection string into the environment variables
     DATABASE_URL=postgresql://...@.../my-saas-db
```

---

### Step 2: Connection string injected automatically at deploy time

```go
// server/internal/deploy/orchestrator.go
func (o *Orchestrator) execute(...) error {
    // look up the user's database info (from Layer 1)
    var dbID, dbName string
    err := o.db.QueryRow(ctx, `
        SELECT user_database_id, user_database_name 
        FROM projects WHERE id = $1 AND team_id = $2  -- ✅ add team_id!
    `, plan.ProjectID, teamID).Scan(&dbID, &dbName)
    
    // get the connection string from Neon
    connURI, err := o.neon.GetConnectionURI(ctx, dbID, dbName)
    
    // inject into the environment variables (encrypted in transit)
    for i, svc := range plan.Services {
        for j, ev := range svc.EnvVars {
            if ev.Classification == EnvAutoInject && ev.Source == "provision_postgres" {
                plan.Services[i].EnvVars[j].Value = connURI
            }
        }
    }
}
```

---

### Step 3: The user's application uses DATABASE_URL

```env
# the user's .env (injected automatically by LaunchKit)
DATABASE_URL=postgresql://user:pass@ep-abc123.neon.tech/my-saas-db
```

The user's code has no idea LaunchKit exists and just uses the standard `DATABASE_URL`.

---

## 🔐 Security Comparison

### Wrong Design (all data together)

```
After a hacker succeeds with SQL injection:

SELECT * FROM users;     -- all users' data leaked
SELECT * FROM orders;    -- all orders leaked
SELECT * FROM payments;  -- all payments leaked

Impact: 100% of user data leaked
```

### Right Design (physical isolation)

```
After a hacker succeeds with SQL injection:

SELECT * FROM users;     -- only Xiao Wang's my-saas user data
SELECT * FROM orders;    -- only Xiao Wang's my-saas orders
SELECT * FROM payments;  -- only Xiao Wang's my-saas payments

Impact: only one of Xiao Wang's projects is leaked; other users are unaffected
```

---

## 📋 Database Configuration Examples

| Layer | Purpose | Provider | Isolation | Example |
|------|------|--------|----------|------|
| **Layer 1** | LaunchKit platform data | Neon Postgres | Logical isolation by team_id | `launchkit_platform` |
| **Layer 2** | User Project A data | Neon Database | Physical isolation | `neon-my-saas-abc123` |
| **Layer 2** | User Project B data | Neon Database | Physical isolation | `neon-ecommerce-xyz789` |
| **Layer 2** | User Project C data | Neon Database | Physical isolation | `neon-api-db456` |

---

## 💰 Cost Allocation

### Layer 1: Platform Database

| Item | Cost | Borne by |
|------|------|--------|
| Neon Compute | $0-50/mo | LaunchKit |
| Neon Storage | $0-20/mo | LaunchKit |
| Backups | $0-10/mo | LaunchKit |
| **Total** | **$0-80/mo** | **LaunchKit** |

### Layer 2: User Project Databases

| Item | Cost | Borne by |
|------|------|--------|
| Neon Compute | $0-19/mo per project | User (billed through LaunchKit) |
| Neon Storage | $0-10/mo per project | User (billed through LaunchKit) |
| Backups | $0-5/mo per project | User (billed through LaunchKit) |
| **Total (100 projects)** | **$0-3400/mo** | **User ($5000/mo after markup)** |

**LaunchKit profit**: what users pay - actual cost = **$1600/mo+**

---

## 🎯 Summary

1. **Layer 1 (platform database)**: sharing is reasonable because it stores only metadata and needs `team_id` isolation
2. **Layer 2 (user databases)**: must be physically isolated, one Neon Database per project
3. **Security advantage**: even with SQL injection only a single project is affected, with no lateral movement
4. **Compliance advantage**: GDPR data deletion = dropping the whole database, simple and clean
5. **Business advantage**: billing per project, high profit margin

This design is a **true multi-tenant enterprise-grade architecture**.
