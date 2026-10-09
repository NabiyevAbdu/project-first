package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("pbc_1930317162")
		if err != nil {
			return err
		}

		// add field
		if err := collection.Fields.AddMarshaledJSONAt(4, []byte(`{
			"hidden": false,
			"id": "select1529437807",
			"maxSelect": 1,
			"name": "confirmation_type",
			"presentable": false,
			"required": false,
			"system": false,
			"type": "select",
			"values": [
				"bot",
				"group",
				"both"
			]
		}`)); err != nil {
			return err
		}

		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("pbc_1930317162")
		if err != nil {
			return err
		}

		// remove field
		collection.Fields.RemoveById("select1529437807")

		return app.Save(collection)
	})
}
