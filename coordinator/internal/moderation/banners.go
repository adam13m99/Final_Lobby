package moderation

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The banner strip (D43): the slide of announcements across the top of the
// lobby, which admins add, remove and edit.
//
// Two constraints are load-bearing and easy to lose later:
//
//   - **A banner is text, an image and a link — nothing executable.** It is
//     content staff write that every player's client renders, which is
//     exactly the shape of a stored cross-site scripting hole if it is ever
//     allowed to carry markup. Whoever writes the front end must render these
//     as text, never as HTML.
//   - **Links are restricted to http and https.** A `javascript:` or `file:`
//     link in a desktop application is a way to run something on a player's
//     machine, and a banner is the one place a person who is not the player
//     chooses what the player clicks.

const (
	MaxBannerTitle = 80
	MaxBannerBody  = 300
)

var (
	ErrBannerEmpty  = errors.New("moderation: a banner needs a title")
	ErrBannerLink   = errors.New("moderation: a banner link must be an http or https address")
	ErrNoSuchBanner = errors.New("moderation: no such banner")
)

// Banner is one slide.
type Banner struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	ImageURL string `json:"image_url,omitempty"`
	LinkURL  string `json:"link_url,omitempty"`
	Sort     int    `json:"sort"`
	Active   bool   `json:"active"`

	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AddBanner creates a slide.
func (s *Store) AddBanner(actorID string, b Banner, now time.Time) (Banner, error) {
	if err := s.RequireStaff(actorID); err != nil {
		return Banner{}, err
	}
	clean, err := cleanBanner(b)
	if err != nil {
		return Banner{}, err
	}
	id, err := newID()
	if err != nil {
		return Banner{}, err
	}
	clean.ID = id
	clean.CreatedBy, clean.UpdatedBy = actorID, actorID
	clean.CreatedAt, clean.UpdatedAt = now.UTC(), now.UTC()

	if _, err := s.db.Exec(
		`INSERT INTO banners (id, title, body, image_url, link_url, sort_order, active,
		                      created_by, created_at, updated_by, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		clean.ID, clean.Title, clean.Body, clean.ImageURL, clean.LinkURL, clean.Sort,
		boolToInt(clean.Active), actorID, stamp(now), actorID, stamp(now)); err != nil {
		return Banner{}, fmt.Errorf("moderation: adding banner: %w", err)
	}
	if err := s.record(actorID, "banner_add", clean.ID, clean.Title, now); err != nil {
		return Banner{}, err
	}
	return clean, nil
}

// EditBanner replaces a slide's content.
func (s *Store) EditBanner(actorID, bannerID string, b Banner, now time.Time) (Banner, error) {
	if err := s.RequireStaff(actorID); err != nil {
		return Banner{}, err
	}
	clean, err := cleanBanner(b)
	if err != nil {
		return Banner{}, err
	}
	res, err := s.db.Exec(
		`UPDATE banners SET title = ?, body = ?, image_url = ?, link_url = ?,
		        sort_order = ?, active = ?, updated_by = ?, updated_at = ?
		 WHERE id = ?`,
		clean.Title, clean.Body, clean.ImageURL, clean.LinkURL, clean.Sort,
		boolToInt(clean.Active), actorID, stamp(now), bannerID)
	if err != nil {
		return Banner{}, fmt.Errorf("moderation: editing banner: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Banner{}, ErrNoSuchBanner
	}
	if err := s.record(actorID, "banner_edit", bannerID, clean.Title, now); err != nil {
		return Banner{}, err
	}
	return s.Banner(bannerID)
}

// RemoveBanner deletes a slide.
func (s *Store) RemoveBanner(actorID, bannerID string, now time.Time) error {
	if err := s.RequireStaff(actorID); err != nil {
		return err
	}
	res, err := s.db.Exec(`DELETE FROM banners WHERE id = ?`, bannerID)
	if err != nil {
		return fmt.Errorf("moderation: removing banner: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchBanner
	}
	return s.record(actorID, "banner_remove", bannerID, "", now)
}

// Banner returns one slide.
func (s *Store) Banner(bannerID string) (Banner, error) {
	row := s.db.QueryRow(
		`SELECT id, title, body, image_url, link_url, sort_order, active,
		        created_by, created_at, updated_by, updated_at
		 FROM banners WHERE id = ?`, bannerID)
	b, err := scanBanner(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Banner{}, ErrNoSuchBanner
	}
	return b, err
}

// Banners lists slides. Pass false for onlyActive to see hidden ones too,
// which is what the admin screen wants and the lobby does not.
func (s *Store) Banners(onlyActive bool) ([]Banner, error) {
	query := `SELECT id, title, body, image_url, link_url, sort_order, active,
	                 created_by, created_at, updated_by, updated_at
	          FROM banners`
	if onlyActive {
		query += ` WHERE active = 1`
	}
	query += ` ORDER BY sort_order, created_at`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("moderation: reading banners: %w", err)
	}
	defer rows.Close()

	out := []Banner{}
	for rows.Next() {
		b, err := scanBanner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanBanner(row scanner) (Banner, error) {
	var (
		b                Banner
		active           int
		created, updated string
	)
	err := row.Scan(&b.ID, &b.Title, &b.Body, &b.ImageURL, &b.LinkURL, &b.Sort, &active,
		&b.CreatedBy, &created, &b.UpdatedBy, &updated)
	if err != nil {
		return Banner{}, err
	}
	b.Active = active != 0
	b.CreatedAt, b.UpdatedAt = parse(created), parse(updated)
	return b, nil
}

func cleanBanner(b Banner) (Banner, error) {
	b.Title = strings.TrimSpace(b.Title)
	b.Body = strings.TrimSpace(b.Body)
	b.ImageURL = strings.TrimSpace(b.ImageURL)
	b.LinkURL = strings.TrimSpace(b.LinkURL)

	if b.Title == "" {
		return Banner{}, ErrBannerEmpty
	}
	if len([]rune(b.Title)) > MaxBannerTitle {
		b.Title = string([]rune(b.Title)[:MaxBannerTitle])
	}
	if len([]rune(b.Body)) > MaxBannerBody {
		b.Body = string([]rune(b.Body)[:MaxBannerBody])
	}
	for _, u := range []string{b.LinkURL, b.ImageURL} {
		if !safeURL(u) {
			return Banner{}, ErrBannerLink
		}
	}
	return b, nil
}

// safeURL allows only http and https.
//
// A banner is the one place somebody who is not the player chooses what the
// player clicks, and this is a desktop application. A "javascript:" or "file:"
// link there is a way to run something on their machine.
func safeURL(u string) bool {
	if u == "" {
		return true
	}
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
