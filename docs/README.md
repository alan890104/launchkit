# LaunchKit Documentation Index

> **Last updated**: 2026-04-02  
> **Version**: 0.1.0

---

## 📚 Core Docs

| Doc | Description | Audience |
|------|------|----------|
| [USER_FLOW.md](USER_FLOW.md) | Complete user flow design | Product, Engineering |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Technical architecture overview | Engineering |
| [skypilot.md](skypilot.md) | GPU orchestration implementation details | Engineering |

---

## 🏗️ Architecture Design Docs (`architecture/`)

| Doc | Description | Importance |
|------|------|----------|
| [DATABASE_ISOLATION_ARCHITECTURE.md](architecture/DATABASE_ISOLATION_ARCHITECTURE.md) | **Two-layer database isolation design** | 🔴 Must read |
| [ENTERPRISE_MONITORING.md](architecture/ENTERPRISE_MONITORING.md) | Enterprise monitoring, alerting, and environment management | 🟠 Recommended |

**Suggested reading order**:
1. Start with [ARCHITECTURE.md](ARCHITECTURE.md) for the overall architecture
2. Then read [DATABASE_ISOLATION_ARCHITECTURE.md](architecture/DATABASE_ISOLATION_ARCHITECTURE.md) for the database isolation design
3. Finally read [ENTERPRISE_MONITORING.md](architecture/ENTERPRISE_MONITORING.md) for the monitoring and alerting system

---

## 📖 Usage Docs (`usage/`)

| Doc | Description | Audience |
|------|------|----------|
| [MCP_TOOLS_USAGE_EXAMPLES.md](usage/MCP_TOOLS_USAGE_EXAMPLES.md) | Usage examples for 21 MCP tools | Engineering, Users |

**MCP Tools categories**:
- **Core deployment** (4): `plan_deployment`, `deploy_project`, `deploy_status`, `deploy_logs`
- **Project management** (5): `list_projects`, `destroy`, `scale`, `restart`, `usage`
- **Environment management** (4): `create_environment`, `list_environments`, `promote_environment`, `destroy_environment`
- **Monitoring and alerting** (6): `get_metrics`, `set_alert`, `list_alerts`, `delete_alert`, `get_incidents`, `get_uptime`

---

## 🗺️ Doc Navigation

```
launchkit/
├── docs/
│   ├── README.md                   ← this file (docs index)
│   │
│   ├── USER_FLOW.md                ← user flow (start here)
│   ├── ARCHITECTURE.md             ← technical architecture (must read)
│   ├── skypilot.md                 ← GPU orchestration
│   │
│   ├── architecture/
│   │   ├── DATABASE_ISOLATION_ARCHITECTURE.md  ← database isolation (must read)
│   │   └── ENTERPRISE_MONITORING.md            ← monitoring and alerting system
│   │
│   ├── security/
│   │   └── (removed)
│   │
│   └── usage/
│       └── MCP_TOOLS_USAGE_EXAMPLES.md         ← MCP tools usage examples
│
└── server/                         ← code
    ├── internal/
    └── ...
```

---

## 📋 Suggested Reading Paths

### New engineers

```
1. docs/USER_FLOW.md          → understand the user experience goals
2. docs/ARCHITECTURE.md       → understand the overall architecture
3. docs/architecture/DATABASE_ISOLATION_ARCHITECTURE.md → understand the database design
4. docs/usage/MCP_TOOLS_USAGE_EXAMPLES.md → understand tool usage
```

### Product managers

```
1. docs/USER_FLOW.md          → user flow
2. docs/usage/MCP_TOOLS_USAGE_EXAMPLES.md → feature list
```

### Security auditors

```
1. docs/architecture/DATABASE_ISOLATION_ARCHITECTURE.md → isolation design
```

---

## 📝 Documentation Guidelines

1. **Adding a doc**: it must be added to the index in `docs/README.md`
2. **Updating a doc**: if it affects other docs, update the related indexes
3. **Deleting a doc**: make sure its content has been merged into other docs, and update the indexes
4. **Naming**: use the `SNAKE_CASE.md` format
5. **Categories**:
   - Core docs → `docs/` (USER_FLOW, ARCHITECTURE, skypilot)
   - Architecture design → `docs/architecture/`
   - Usage guides → `docs/usage/`
   - API reference → `docs/api/` (to be added later)
