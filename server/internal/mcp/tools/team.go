package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterTeam(srv *server.MCPServer, deps *Deps) {
	RegisterInviteMember(srv, deps)
	RegisterListMembers(srv, deps)
	RegisterUpdateMemberRole(srv, deps)
	RegisterRemoveMember(srv, deps)
}

// ─────────────────────────────────────────────────────────────────────────────
// invite_member
// ─────────────────────────────────────────────────────────────────────────────

func RegisterInviteMember(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("invite_member",
			mcp.WithDescription(
				"Invite a new member to your team by email. "+
					"The invited user will be created if they don't exist yet.",
			),
			mcp.WithString("email",
				mcp.Required(),
				mcp.Description("Email address of the person to invite"),
			),
			mcp.WithString("role",
				mcp.Required(),
				mcp.Description("Role for the new member: admin, deployer, or viewer"),
			),
		),
		makeInviteMemberHandler(deps),
	)
}

func makeInviteMemberHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		email, ok := args["email"].(string)
		if !ok || email == "" {
			return mcp.NewToolResultError("email is required"), nil
		}

		role, ok := args["role"].(string)
		if !ok || role == "" {
			return mcp.NewToolResultError("role is required"), nil
		}
		if role != "admin" && role != "deployer" && role != "viewer" {
			return mcp.NewToolResultError("role must be one of: admin, deployer, viewer"), nil
		}

		// Get caller's team (must be owner)
		var teamID string
		err := deps.DB.QueryRow(ctx, `
			SELECT team_id FROM team_members
			WHERE user_id = $1 AND role = 'owner'
			LIMIT 1
		`, scope.UserID).Scan(&teamID)
		if err != nil {
			return mcp.NewToolResultError("you must be a team owner to invite members"), nil
		}

		// Look up or create the invited user by email
		var invitedUserID, invitedName string
		err = deps.DB.QueryRow(ctx, `
			SELECT id, COALESCE(name, '') FROM users WHERE email = $1
		`, email).Scan(&invitedUserID, &invitedName)
		if err != nil {
			// User doesn't exist — create a placeholder
			invitedUserID = fmt.Sprintf("pending_%s", email)
			_, err = deps.DB.Exec(ctx, `
				INSERT INTO users (id, email) VALUES ($1, $2)
				ON CONFLICT (email) DO NOTHING
			`, invitedUserID, email)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("create invited user: %v", err)), nil
			}
			// Re-read in case of race (ON CONFLICT)
			_ = deps.DB.QueryRow(ctx, `
				SELECT id, COALESCE(name, '') FROM users WHERE email = $1
			`, email).Scan(&invitedUserID, &invitedName)
		}

		// Insert team membership
		_, err = deps.DB.Exec(ctx, `
			INSERT INTO team_members (team_id, user_id, role, invited_by)
			VALUES ($1, $2, $3, $4)
		`, teamID, invitedUserID, role, scope.UserID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invite member: %v (user may already be a member)", err)), nil
		}

		WriteAuditLog(ctx, deps, teamID, scope.UserID, "invite_member", "team", teamID, map[string]any{
			"invited_email": email,
			"role":          role,
		})

		result, _ := json.Marshal(map[string]any{
			"status":  "invited",
			"email":   email,
			"name":    invitedName,
			"user_id": invitedUserID,
			"role":    role,
			"team_id": teamID,
			"message": fmt.Sprintf("Successfully invited %s as %s.", email, role),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// list_members
// ─────────────────────────────────────────────────────────────────────────────

func RegisterListMembers(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("list_members",
			mcp.WithDescription(
				"List all members of your team with their roles.",
			),
		),
		makeListMembersHandler(deps),
	)
}

func makeListMembersHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// [FIX VULN-005] Get the caller's primary team — prefer the team where the user
		// is 'owner' to guarantee deterministic results for multi-team members.
		var teamID, teamName string
		err := deps.DB.QueryRow(ctx, `
			SELECT t.id, t.name
			FROM teams t
			JOIN team_members tm ON t.id = tm.team_id
			WHERE tm.user_id = $1
			ORDER BY
				CASE tm.role WHEN 'owner' THEN 0 ELSE 1 END,
				t.created_at ASC
			LIMIT 1
		`, scope.UserID).Scan(&teamID, &teamName)
		if err != nil {
			return mcp.NewToolResultError("could not find your team"), nil
		}

		// Query all members
		rows, err := deps.DB.Query(ctx, `
			SELECT u.id, u.email, COALESCE(u.name, ''), tm.role
			FROM team_members tm
			JOIN users u ON tm.user_id = u.id
			WHERE tm.team_id = $1
			ORDER BY
				CASE tm.role
					WHEN 'owner' THEN 0
					WHEN 'admin' THEN 1
					WHEN 'deployer' THEN 2
					WHEN 'viewer' THEN 3
				END
		`, teamID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query members: %v", err)), nil
		}
		defer rows.Close()

		type memberInfo struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Name  string `json:"name"`
			Role  string `json:"role"`
		}

		var members []memberInfo
		for rows.Next() {
			var m memberInfo
			if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.Role); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("scan member: %v", err)), nil
			}
			members = append(members, m)
		}
		if err := rows.Err(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("iterate members: %v", err)), nil
		}

		result, _ := json.Marshal(map[string]any{
			"team":          teamName,
			"team_id":       teamID,
			"members":       members,
			"total_members": len(members),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// update_member_role
// ─────────────────────────────────────────────────────────────────────────────

func RegisterUpdateMemberRole(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("update_member_role",
			mcp.WithDescription(
				"Update a team member's role. Only owners and admins can change roles. "+
					"The owner role cannot be changed.",
			),
			mcp.WithString("email",
				mcp.Required(),
				mcp.Description("Email of the member whose role to update"),
			),
			mcp.WithString("role",
				mcp.Required(),
				mcp.Description("New role: admin, deployer, or viewer"),
			),
		),
		makeUpdateMemberRoleHandler(deps),
	)
}

func makeUpdateMemberRoleHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		email, ok := args["email"].(string)
		if !ok || email == "" {
			return mcp.NewToolResultError("email is required"), nil
		}

		role, ok := args["role"].(string)
		if !ok || role == "" {
			return mcp.NewToolResultError("role is required"), nil
		}
		if role != "admin" && role != "deployer" && role != "viewer" {
			return mcp.NewToolResultError("role must be one of: admin, deployer, viewer"), nil
		}

		// Get caller's team and verify they are owner or admin
		var teamID, callerRole string
		err := deps.DB.QueryRow(ctx, `
			SELECT team_id, role FROM team_members
			WHERE user_id = $1 AND role IN ('owner', 'admin')
			LIMIT 1
		`, scope.UserID).Scan(&teamID, &callerRole)
		if err != nil {
			return mcp.NewToolResultError("you must be a team owner or admin to update member roles"), nil
		}

		// Look up the target user
		var targetUserID, targetName string
		err = deps.DB.QueryRow(ctx, `
			SELECT id, COALESCE(name, '') FROM users WHERE email = $1
		`, email).Scan(&targetUserID, &targetName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("user with email %q not found", email)), nil
		}

		// Check the target member's current role
		var currentRole string
		err = deps.DB.QueryRow(ctx, `
			SELECT role FROM team_members
			WHERE team_id = $1 AND user_id = $2
		`, teamID, targetUserID).Scan(&currentRole)
		if err != nil {
			return mcp.NewToolResultError("that user is not a member of your team"), nil
		}

		if currentRole == "owner" {
			return mcp.NewToolResultError("cannot change the owner's role"), nil
		}

		// Update the role
		_, err = deps.DB.Exec(ctx, `
			UPDATE team_members SET role = $1
			WHERE team_id = $2 AND user_id = $3
		`, role, teamID, targetUserID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("update role: %v", err)), nil
		}

		result, _ := json.Marshal(map[string]any{
			"status":        "updated",
			"email":         email,
			"name":          targetName,
			"user_id":       targetUserID,
			"previous_role": currentRole,
			"new_role":      role,
			"message":       fmt.Sprintf("Updated %s from %s to %s.", email, currentRole, role),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// remove_member
// ─────────────────────────────────────────────────────────────────────────────

func RegisterRemoveMember(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("remove_member",
			mcp.WithDescription(
				"Remove a member from your team. Only owners and admins can remove members. "+
					"The team owner cannot be removed.",
			),
			mcp.WithString("email",
				mcp.Required(),
				mcp.Description("Email of the member to remove"),
			),
		),
		makeRemoveMemberHandler(deps),
	)
}

func makeRemoveMemberHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		email, ok := args["email"].(string)
		if !ok || email == "" {
			return mcp.NewToolResultError("email is required"), nil
		}

		// Get caller's team and verify they are owner or admin
		var teamID, callerRole string
		err := deps.DB.QueryRow(ctx, `
			SELECT team_id, role FROM team_members
			WHERE user_id = $1 AND role IN ('owner', 'admin')
			LIMIT 1
		`, scope.UserID).Scan(&teamID, &callerRole)
		if err != nil {
			return mcp.NewToolResultError("you must be a team owner or admin to remove members"), nil
		}

		// Look up the target user
		var targetUserID string
		err = deps.DB.QueryRow(ctx, `
			SELECT id FROM users WHERE email = $1
		`, email).Scan(&targetUserID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("user with email %q not found", email)), nil
		}

		// Check the target member's current role
		var currentRole string
		err = deps.DB.QueryRow(ctx, `
			SELECT role FROM team_members
			WHERE team_id = $1 AND user_id = $2
		`, teamID, targetUserID).Scan(&currentRole)
		if err != nil {
			return mcp.NewToolResultError("that user is not a member of your team"), nil
		}

		if currentRole == "owner" {
			return mcp.NewToolResultError("cannot remove the team owner"), nil
		}

		// Delete the membership
		_, err = deps.DB.Exec(ctx, `
			DELETE FROM team_members
			WHERE team_id = $1 AND user_id = $2
		`, teamID, targetUserID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("remove member: %v", err)), nil
		}

		WriteAuditLog(ctx, deps, teamID, scope.UserID, "remove_member", "team", teamID, map[string]any{
			"removed_email": email,
			"removed_role":  currentRole,
		})

		result, _ := json.Marshal(map[string]any{
			"status":  "removed",
			"email":   email,
			"user_id": targetUserID,
			"role":    currentRole,
			"message": fmt.Sprintf("Removed %s (%s) from the team.", email, currentRole),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}
