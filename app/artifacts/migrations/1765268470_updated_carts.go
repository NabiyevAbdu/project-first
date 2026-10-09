package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("pbc_2236019783")
		if err != nil {
			return err
		}

		// add field
		if err := collection.Fields.AddMarshaledJSONAt(2, []byte(`{
			"cascadeDelete": false,
			"collectionId": "pbc_4092854851",
			"hidden": false,
			"id": "relation3544843437",
			"maxSelect": 1,
			"minSelect": 0,
			"name": "product",
			"presentable": false,
			"required": false,
			"system": false,
			"type": "relation"
		}`)); err != nil {
			return err
		}

		// add field
		if err := collection.Fields.AddMarshaledJSONAt(3, []byte(`{
			"hidden": false,
			"id": "number2683508278",
			"max": null,
			"min": null,
			"name": "quantity",
			"onlyInt": false,
			"presentable": false,
			"required": false,
			"system": false,
			"type": "number"
		}`)); err != nil {
			return err
		}

		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("pbc_2236019783")
		if err != nil {
			return err
		}

		// remove field
		collection.Fields.RemoveById("relation3544843437")

		// remove field
		collection.Fields.RemoveById("number2683508278")

		return app.Save(collection)
	})
}
