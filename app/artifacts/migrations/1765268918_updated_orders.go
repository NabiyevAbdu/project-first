package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("pbc_3527180448")
		if err != nil {
			return err
		}

		// add field
		if err := collection.Fields.AddMarshaledJSONAt(4, []byte(`{
			"hidden": false,
			"id": "select3618974224",
			"maxSelect": 1,
			"name": "statys",
			"presentable": false,
			"required": false,
			"system": false,
			"type": "select",
			"values": [
				"Done",
				"In progress"
			]
		}`)); err != nil {
			return err
		}

		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("pbc_3527180448")
		if err != nil {
			return err
		}

		// remove field
		collection.Fields.RemoveById("select3618974224")

		return app.Save(collection)
	})
}
