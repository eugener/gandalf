package sqlite

import (
	"context"
	"database/sql"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

// CreateOrg inserts a new organization.
func (s *Store) CreateOrg(ctx context.Context, org *gateway.Organization) error {
	models, err := marshalJSON(org.AllowedModels)
	if err != nil {
		return err
	}
	_, err = s.write.ExecContext(ctx,
		`INSERT INTO organizations (id, name, allowed_models, rpm_limit, tpm_limit, max_budget, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		org.ID, org.Name, models, org.RPMLimit, org.TPMLimit, org.MaxBudget,
		org.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// GetOrg retrieves an organization by ID.
func (s *Store) GetOrg(ctx context.Context, id string) (*gateway.Organization, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT id, name, allowed_models, rpm_limit, tpm_limit, max_budget, created_at
		 FROM organizations WHERE id=?`, id,
	)
	return scanOrg(row)
}

// ListOrgs returns all organizations.
func (s *Store) ListOrgs(ctx context.Context, offset, limit int) ([]*gateway.Organization, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, name, allowed_models, rpm_limit, tpm_limit, max_budget, created_at
		 FROM organizations ORDER BY name LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orgs []*gateway.Organization
	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			return nil, err
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

// UpdateOrg updates an organization.
func (s *Store) UpdateOrg(ctx context.Context, org *gateway.Organization) error {
	models, err := marshalJSON(org.AllowedModels)
	if err != nil {
		return err
	}
	result, err := s.write.ExecContext(ctx,
		`UPDATE organizations SET name=?, allowed_models=?, rpm_limit=?, tpm_limit=?, max_budget=?
		 WHERE id=?`,
		org.Name, models, org.RPMLimit, org.TPMLimit, org.MaxBudget, org.ID,
	)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "organization")
}

// DeleteOrg removes an organization.
func (s *Store) DeleteOrg(ctx context.Context, id string) error {
	result, err := s.write.ExecContext(ctx, `DELETE FROM organizations WHERE id=?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "organization")
}

// CreateTeam inserts a new team.
func (s *Store) CreateTeam(ctx context.Context, team *gateway.Team) error {
	models, err := marshalJSON(team.AllowedModels)
	if err != nil {
		return err
	}
	_, err = s.write.ExecContext(ctx,
		`INSERT INTO teams (id, org_id, name, allowed_models, rpm_limit, tpm_limit, max_budget)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		team.ID, team.OrgID, team.Name, models, team.RPMLimit, team.TPMLimit, team.MaxBudget,
	)
	return err
}

// GetTeam retrieves a team by ID.
func (s *Store) GetTeam(ctx context.Context, id string) (*gateway.Team, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT id, org_id, name, allowed_models, rpm_limit, tpm_limit, max_budget
		 FROM teams WHERE id=?`, id,
	)
	return scanTeam(row)
}

// ListTeams returns all teams in an organization.
func (s *Store) ListTeams(ctx context.Context, orgID string, offset, limit int) ([]*gateway.Team, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, org_id, name, allowed_models, rpm_limit, tpm_limit, max_budget
		 FROM teams WHERE org_id=? ORDER BY name LIMIT ? OFFSET ?`, orgID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []*gateway.Team
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// UpdateTeam updates a team.
func (s *Store) UpdateTeam(ctx context.Context, team *gateway.Team) error {
	models, err := marshalJSON(team.AllowedModels)
	if err != nil {
		return err
	}
	result, err := s.write.ExecContext(ctx,
		`UPDATE teams SET name=?, allowed_models=?, rpm_limit=?, tpm_limit=?, max_budget=?
		 WHERE id=?`,
		team.Name, models, team.RPMLimit, team.TPMLimit, team.MaxBudget, team.ID,
	)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "team")
}

// DeleteTeam removes a team.
func (s *Store) DeleteTeam(ctx context.Context, id string) error {
	result, err := s.write.ExecContext(ctx, `DELETE FROM teams WHERE id=?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "team")
}

func scanOrg(s scanner) (*gateway.Organization, error) {
	var o gateway.Organization
	var modelsJSON sql.NullString
	var createdAt sql.NullString

	err := s.Scan(&o.ID, &o.Name, &modelsJSON, &o.RPMLimit, &o.TPMLimit, &o.MaxBudget, &createdAt)
	if err != nil {
		return nil, notFoundErr(err)
	}

	models, err := unmarshalStringSlice(modelsJSON)
	if err != nil {
		return nil, err
	}
	o.AllowedModels = models
	if t := parseTime(createdAt); t != nil {
		o.CreatedAt = *t
	}
	return &o, nil
}

func scanTeam(s scanner) (*gateway.Team, error) {
	var t gateway.Team
	var modelsJSON sql.NullString

	err := s.Scan(&t.ID, &t.OrgID, &t.Name, &modelsJSON, &t.RPMLimit, &t.TPMLimit, &t.MaxBudget)
	if err != nil {
		return nil, notFoundErr(err)
	}

	models, err := unmarshalStringSlice(modelsJSON)
	if err != nil {
		return nil, err
	}
	t.AllowedModels = models
	return &t, nil
}
