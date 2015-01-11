package bongo

import (
	"errors"
	"github.com/maxwellhealth/dotaccess"
	"github.com/oleiade/reflections"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"strings"
)

// Relation types (one-to-many or one-to-one)
const (
	REL_MANY = iota
	REL_ONE  = iota
)

// Configuration to tell Bongo how to cascade data to related documents on save or delete
type CascadeConfig struct {
	// The collection to cascade to
	Collection *Collection

	// The relation type (does the target doc have an array of these docs [REL_MANY] or just reference a single doc [REL_ONE])
	RelType int

	// The property on the related doc to populate
	ThroughProp string

	// The query to find related docs
	Query bson.M

	// The data that constructs the query may have changed - this is to remove self from previous relations
	OldQuery bson.M

	// Should it also cascade the related doc on save?
	Nest bool

	// Data to cascade. Can be in dot notation
	Properties []string

	// An instance of the related doc if it needs to be nested
	Instance interface{}
}

// Cascades a document's properties to related documents, after it has been prepared
// for db insertion (encrypted, etc)
func CascadeSave(doc interface{}, preparedForSave map[string]interface{}) {
	// Find out which properties to cascade
	if conv, ok := doc.(interface {
		GetCascade() []*CascadeConfig
	}); ok {
		toCascade := conv.GetCascade()

		for _, conf := range toCascade {
			cascadeSaveWithConfig(conf, preparedForSave)

			if conf.Nest {
				results := conf.Collection.Find(conf.Query)

				for results.Next(conf.Instance) {
					prepared := conf.Collection.PrepDocumentForSave(conf.Instance)
					CascadeSave(conf.Instance, prepared)
				}

			}
		}
	}
}

// Deletes references to a document from its related documents
func CascadeDelete(doc interface{}) {
	// Find out which properties to cascade
	if conv, ok := doc.(interface {
		GetCascade() []*CascadeConfig
	}); ok {
		toCascade := conv.GetCascade()

		// Get the ID
		id, err := reflections.GetField(doc, "Id")

		if err != nil {
			panic(err)
		}

		// Cast as bson.ObjectId
		if bsonId, ok := id.(bson.ObjectId); ok {
			for _, conf := range toCascade {
				cascadeDeleteWithConfig(conf, bsonId)
			}
		}

	}
}

// Runs a cascaded delete operation with one configuration
func cascadeDeleteWithConfig(conf *CascadeConfig, id bson.ObjectId) (*mgo.ChangeInfo, error) {
	switch conf.RelType {
	case REL_ONE:
		update := map[string]map[string]interface{}{
			"$set": map[string]interface{}{},
		}

		if len(conf.ThroughProp) > 0 {
			update["$set"][conf.ThroughProp] = nil
		} else {
			for _, p := range conf.Properties {
				update["$set"][p] = nil
			}
		}

		return conf.Collection.Collection().UpdateAll(conf.Query, update)
	case REL_MANY:
		update := map[string]map[string]interface{}{
			"$pull": map[string]interface{}{},
		}

		update["$pull"][conf.ThroughProp] = bson.M{
			"_id": id,
		}
		return conf.Collection.Collection().UpdateAll(conf.Query, update)
	}

	return &mgo.ChangeInfo{}, errors.New("Invalid relation type")
}

// Runs a cascaded save operation with one configuration
func cascadeSaveWithConfig(conf *CascadeConfig, preparedForSave map[string]interface{}) (*mgo.ChangeInfo, error) {
	// Create a new map with just the props to cascade

	id := preparedForSave["_id"]

	data := make(map[string]interface{})
	// Set the id field automatically if there's a through prop
	if len(conf.ThroughProp) > 0 {
		data["_id"] = id
	}

	for _, prop := range conf.Properties {
		split := strings.Split(prop, ".")

		if len(split) == 1 {
			data[prop] = preparedForSave[prop]
		} else {
			actualProp := split[len(split)-1]
			split := append([]string{}, split[:len(split)-1]...)
			curData := data

			for _, s := range split {
				if _, ok := curData[s]; ok {
					if mapped, ok := curData[s].(map[string]interface{}); ok {
						curData = mapped
					} else {
						panic("Cannot access non-map property via dot notationa")
					}

				} else {
					curData[s] = make(map[string]interface{})
					if mapped, ok := curData[s].(map[string]interface{}); ok {
						curData = mapped
					} else {
						panic("Cannot access non-map property via dot notationb")
					}
				}
			}

			curData[actualProp], _ = dotaccess.Get(preparedForSave, prop)
		}

	}

	switch conf.RelType {
	case REL_ONE:
		if len(conf.OldQuery) > 0 {

			update1 := map[string]map[string]interface{}{
				"$set": map[string]interface{}{},
			}

			if len(conf.ThroughProp) > 0 {
				update1["$set"][conf.ThroughProp] = nil
			} else {
				for _, p := range conf.Properties {
					update1["$set"][p] = nil
				}
			}
			conf.Collection.Collection().UpdateAll(conf.OldQuery, update1)
		}

		update := map[string]map[string]interface{}{
			"$set": map[string]interface{}{},
		}

		if len(conf.ThroughProp) > 0 {
			update["$set"][conf.ThroughProp] = data
		} else {
			for k, v := range data {
				update["$set"][k] = v
			}
		}

		// Just update
		return conf.Collection.Collection().UpdateAll(conf.Query, update)
	case REL_MANY:
		update1 := map[string]map[string]interface{}{
			"$pull": map[string]interface{}{},
		}

		update1["$pull"][conf.ThroughProp] = bson.M{
			"_id": id,
		}

		if len(conf.OldQuery) > 0 {
			conf.Collection.Collection().UpdateAll(conf.OldQuery, update1)
		}

		// Remove self from current relations, so we can replace it
		conf.Collection.Collection().UpdateAll(conf.Query, update1)

		update2 := map[string]map[string]interface{}{
			"$push": map[string]interface{}{},
		}

		update2["$push"][conf.ThroughProp] = data
		return conf.Collection.Collection().UpdateAll(conf.Query, update2)

	}

	return &mgo.ChangeInfo{}, errors.New("Invalid relation type")

}
