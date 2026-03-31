package pocketbase

import (
	"fmt"
	"log"

	"github.com/pocketbase/pocketbase/core"
)

func ensureTextField(app core.App, col *core.Collection, name string, required bool) error {
	if col.Fields.GetByName(name) != nil {
		return nil
	}

	col.Fields.Add(&core.TextField{Name: name, Required: required})
	if err := app.Save(col); err != nil {
		return fmt.Errorf("add %q field to %s: %w", name, col.Name, err)
	}
	return nil
}


// InitCollections ensures all application-specific PocketBase collections and
// schema extensions exist. It is idempotent: fields/collections that already
// exist are left untouched.
func InitCollections(app core.App) error {
	if err := ensureUsersRoleField(app); err != nil {
		return err
	}
	if err := ensureBinariesCollection(app); err != nil {
		return err
	}
	if err := ensureSessionsCollection(app); err != nil {
		return err
	}
	return nil
}

// ensureUsersRoleField adds a "role" select field (user|admin) to the built-in
// users collection if it does not already exist.
func ensureUsersRoleField(app core.App) error {
	col, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		// users collection doesn't exist yet (first run before PocketBase init);
		// PocketBase will create it on bootstrap — we skip for now.
		log.Printf("[pocketbase] users collection not found, skipping role field: %v", err)
		return nil
	}

	if col.Fields.GetByName("role") != nil {
		return nil // already present
	}

	col.Fields.Add(&core.SelectField{
		Name:      "role",
		Values:    []string{"user", "admin"},
		MaxSelect: 1,
	})

	if err := app.Save(col); err != nil {
		return fmt.Errorf("add role field to users: %w", err)
	}
	log.Println("[pocketbase] added 'role' field to users collection")
	return nil
}

// ensureBinariesCollection creates the "binaries" collection if it does not exist.
func ensureBinariesCollection(app core.App) error {
	if col, err := app.FindCollectionByNameOrId("binaries"); err == nil {
		return ensureTextField(app, col, "payload", true)
	}

	col := core.NewBaseCollection("binaries")
	col.Fields.Add(
		&core.TextField{Name: "name", Required: true},
		&core.NumberField{Name: "bits", Required: true},
		&core.BoolField{Name: "is_static"},
		&core.TextField{Name: "zephyr_version"},
		&core.TextField{Name: "file_path", Required: true},
		&core.NumberField{Name: "file_size"},
		&core.TextField{Name: "checksum"},
		&core.TextField{Name: "payload", Required: true},
	)

	if err := app.Save(col); err != nil {
		return fmt.Errorf("create binaries collection: %w", err)
	}
	log.Println("[pocketbase] created 'binaries' collection")
	return nil
}

// ensureSessionsCollection creates the "sessions" collection if it does not exist.
func ensureSessionsCollection(app core.App) error {
	if col, err := app.FindCollectionByNameOrId("sessions"); err == nil {
		return ensureTextField(app, col, "payload", true)
	}

	usersColl, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return fmt.Errorf("find users collection for sessions relation: %w", err)
	}

	col := core.NewBaseCollection("sessions")
	col.Fields.Add(
		&core.TextField{Name: "binary_id", Required: true},
		&core.SelectField{
			Name:      "state",
			Values:    []string{"running", "paused", "stopped"},
			MaxSelect: 1,
			Required:  true,
		},
		&core.NumberField{Name: "seed"},
		&core.BoolField{Name: "use_real_time"},
		&core.TextField{Name: "container_id"},
		&core.NumberField{Name: "timeout_seconds"},
		&core.NumberField{Name: "uptime"},
		// Ownership fields for auth/RBAC.
		&core.TextField{Name: "owner_type"}, // "anonymous" | "user"
		&core.TextField{Name: "owner_id"},   // anon UUID or PocketBase user ID
		&core.RelationField{
			Name:         "user",
			CollectionId: usersColl.Id,
		},
		&core.TextField{Name: "payload", Required: true},
	)

	if err := app.Save(col); err != nil {
		return fmt.Errorf("create sessions collection: %w", err)
	}
	log.Println("[pocketbase] created 'sessions' collection")
	return nil
}
