package textiplex_test

import (
	"slices"
	"testing"

	"github.com/RogueTeam/textiplex"
	"github.com/RogueTeam/textiplex/fields"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/tokenizer/en"
	"github.com/RogueTeam/textiplex/tokenizer/keyword"
	"github.com/stretchr/testify/assert"
	"github.com/zeebo/xxh3"
)

func TestReader(t *testing.T) {
	assertions := assert.New(t)

	w := textiplex.Writer{
		TemporaryDirectory: testsuite.TempDirectory(t, "tmp-*"),
		Directory:          testsuite.TempDirectory(t, "segments-*"),
	}

	type Gender int8

	const (
		GenderMale Gender = iota
		GenderFemale
	)

	type Person struct {
		Name       string
		PersonalId string
		Address    string
		Country    string
		Age        int
		Gender     Gender
	}

	people := []Person{
		{Name: "Alice Johnson", PersonalId: "ID-001", Address: "123 Maple St", Country: "USA", Age: 28, Gender: GenderFemale},
		{Name: "Bob Smith", PersonalId: "ID-002", Address: "456 Oak Ave", Country: "UK", Age: 34, Gender: GenderMale},
		{Name: "David Ruiz", PersonalId: "ID-003", Address: "789 Pine Rd", Country: "Spain", Age: 42, Gender: GenderMale},
		{Name: "Diana Prince", PersonalId: "ID-004", Address: "321 Cedar Ln", Country: "Greece", Age: 31, Gender: GenderFemale},
		{Name: "Elena Rossi", PersonalId: "ID-005", Address: "654 Birch Dr", Country: "Italy", Age: 25, Gender: GenderFemale},
		{Name: "Frank Miller", PersonalId: "ID-006", Address: "987 Elm St", Country: "Germany", Age: 50, Gender: GenderMale},
		{Name: "Grace Hopper", PersonalId: "ID-007", Address: "159 Willow Ct", Country: "USA", Age: 45, Gender: GenderFemale},
		{Name: "Henry Ford", PersonalId: "ID-008", Address: "753 Aspen Way", Country: "USA", Age: 62, Gender: GenderMale},
		{Name: "Ivy League", PersonalId: "ID-009", Address: "951 Spruce St", Country: "Canada", Age: 22, Gender: GenderFemale},
		{Name: "Jack White", PersonalId: "ID-010", Address: "357 Poplar Dr", Country: "Australia", Age: 39, Gender: GenderMale},
		{Name: "Karen Chen", PersonalId: "ID-011", Address: "159 Walnut Ave", Country: "China", Age: 29, Gender: GenderFemale},
		{Name: "Liam Neeson", PersonalId: "ID-012", Address: "753 Cherry St", Country: "Ireland", Age: 55, Gender: GenderMale},
		{Name: "Mia Khalifa", PersonalId: "ID-013", Address: "852 Palm Blvd", Country: "Lebanon", Age: 30, Gender: GenderFemale},
		{Name: "Noah Scott", PersonalId: "ID-014", Address: "951 Alder St", Country: "Canada", Age: 40, Gender: GenderMale},
		{Name: "Olivia Wilde", PersonalId: "ID-015", Address: "357 Sycamore Dr", Country: "USA", Age: 37, Gender: GenderFemale},
		{Name: "Paul Atreides", PersonalId: "ID-016", Address: "147 Juniper Ln", Country: "Arrakis", Age: 24, Gender: GenderMale},
		{Name: "Quinn Fabray", PersonalId: "ID-017", Address: "258 Dogwood St", Country: "USA", Age: 21, Gender: GenderFemale},
		{Name: "Ryan Gosling", PersonalId: "ID-018", Address: "369 Birch Rd", Country: "Canada", Age: 44, Gender: GenderMale},
		{Name: "Sara Connor", PersonalId: "ID-019", Address: "741 Oak St", Country: "USA", Age: 33, Gender: GenderFemale},
		{Name: "Tom Hardy", PersonalId: "ID-020", Address: "852 Pine Ct", Country: "UK", Age: 47, Gender: GenderMale},
		{Name: "Ursula K. LeGuin", PersonalId: "ID-021", Address: "963 Cedar Dr", Country: "USA", Age: 88, Gender: GenderFemale},
		{Name: "Victor Hugo", PersonalId: "ID-022", Address: "159 Maple Ave", Country: "France", Age: 75, Gender: GenderMale},
		{Name: "Wendy Darling", PersonalId: "ID-023", Address: "753 Elm St", Country: "UK", Age: 18, Gender: GenderFemale},
		{Name: "Xavier Woods", PersonalId: "ID-024", Address: "951 Willow Rd", Country: "USA", Age: 38, Gender: GenderMale},
		{Name: "Yara Greyjoy", PersonalId: "ID-025", Address: "357 Aspen Ct", Country: "Iron Islands", Age: 27, Gender: GenderFemale},
		{Name: "Zane Grey", PersonalId: "ID-026", Address: "147 Poplar St", Country: "USA", Age: 52, Gender: GenderMale},
		{Name: "Ada Lovelace", PersonalId: "ID-027", Address: "258 Walnut Ln", Country: "UK", Age: 36, Gender: GenderFemale},
		{Name: "Ben Solo", PersonalId: "ID-028", Address: "369 Cherry Dr", Country: "Naboo", Age: 29, Gender: GenderMale},
		{Name: "Clara Oswald", PersonalId: "ID-029", Address: "741 Palm Ave", Country: "UK", Age: 26, Gender: GenderFemale},
		{Name: "David Bowie", PersonalId: "ID-030", Address: "852 Alder St", Country: "UK", Age: 69, Gender: GenderMale},
		{Name: "Eva Green", PersonalId: "ID-031", Address: "963 Sycamore St", Country: "France", Age: 43, Gender: GenderFemale},
		{Name: "Finn Wolfhard", PersonalId: "ID-032", Address: "159 Juniper Dr", Country: "Canada", Age: 23, Gender: GenderMale},
		{Name: "Gina Linetti", PersonalId: "ID-033", Address: "753 Dogwood Ln", Country: "USA", Age: 32, Gender: GenderFemale},
		{Name: "Harry Potter", PersonalId: "ID-034", Address: "951 Birch Ave", Country: "UK", Age: 20, Gender: GenderMale},
		{Name: "Iris West", PersonalId: "ID-035", Address: "357 Oak Rd", Country: "USA", Age: 28, Gender: GenderFemale},
		{Name: "Jon Snow", PersonalId: "ID-036", Address: "147 Pine St", Country: "The North", Age: 30, Gender: GenderMale},
		{Name: "Kira Nerys", PersonalId: "ID-037", Address: "258 Cedar Ct", Country: "Bajor", Age: 35, Gender: GenderFemale},
		{Name: "Luke Skywalker", PersonalId: "ID-038", Address: "369 Aspen St", Country: "Tatooine", Age: 25, Gender: GenderMale},
		{Name: "Margaery Tyrell", PersonalId: "ID-039", Address: "741 Willow Dr", Country: "Highgarden", Age: 24, Gender: GenderFemale},
		{Name: "Nathan Drake", PersonalId: "ID-040", Address: "852 Poplar Ln", Country: "USA", Age: 33, Gender: GenderMale},
	}

	batch := fields.NewBatch(20)
	tokenPool := pool.New[storage.TokenDefinition](20)
	fieldPool := pool.New[storage.FieldDefinition](20)
	for i := range people {
		person := &people[i]

		nameField := fieldPool.Get()
		totalFieldSize := fields.TextField(nameField, tokenPool, "name", []byte(person.Name), en.Tokenizer)
		personalIdField := fieldPool.Get()
		totalFieldSize += fields.TextField(personalIdField, tokenPool, "personal-id", []byte(person.PersonalId), keyword.Tokenizer)
		addressField := fieldPool.Get()
		totalFieldSize += fields.TextField(addressField, tokenPool, "address", []byte(person.Address), en.Tokenizer)
		countryField := fieldPool.Get()
		totalFieldSize += fields.TextField(countryField, tokenPool, "country", []byte(person.Country), en.Tokenizer)
		ageField := fieldPool.Get()
		totalFieldSize += fields.IntegerField(ageField, tokenPool, "age", person.Age)
		genderField := fieldPool.Get()
		totalFieldSize += fields.IntegerField(genderField, tokenPool, "gender", person.Gender)

		batch.Insert(
			storage.DocumentId{Value: storage.RawValueFrom(person.PersonalId)},
			totalFieldSize,
			nameField,
			personalIdField,
			addressField,
			countryField,
			ageField,
			genderField,
		)
	}

	err := w.Batch(batch)
	if !assertions.NoError(err, "failed to insert batch") {
		return
	}

	err = w.Merge()
	if !assertions.NoError(err, "failed to merge writer") {
		return
	}

	type (
		Expect struct {
			DocumentIds []string
		}
		Test struct {
			SortField textiplex.SortField
			Query     string
			Expect    Expect
		}
	)
	tests := []Test{
		{Query: "+Jon +Snow", Expect: Expect{DocumentIds: []string{"ID-036"}}},
		{Query: "+USA", Expect: Expect{DocumentIds: []string{"ID-040", "ID-035", "ID-033", "ID-026", "ID-024", "ID-021", "ID-019", "ID-017", "ID-015", "ID-008", "ID-007", "ID-001"}}},
		{Query: "+Hopper +USA", Expect: Expect{DocumentIds: []string{"ID-007"}}},
		{Query: "+David -country:UK", Expect: Expect{DocumentIds: []string{"ID-003"}}},
	}
	for _, test := range tests {
		t.Run(test.Query, func(t *testing.T) {
			assertions := assert.New(t)

			var reader = textiplex.Reader{
				DefaultTokenizer: en.Tokenizer,
				FieldTokenizers: map[uint64]tokenizer.Tokenizer{
					xxh3.HashString("personal-id"): keyword.Tokenizer,
				},
			}
			err := reader.Reset(w.Directory)
			if !assertions.NoError(err, "failed to reset reader to directory") {
				return
			}
			defer reader.Close()

			seq, err := reader.QueryString(test.SortField, test.Query)
			if !assertions.NoError(err, "failed to query string") {
				return
			}

			if len(test.Expect.DocumentIds) == 0 {
				if !assertions.Empty(slices.Collect(seq), "expecting no results") {
					return
				}
				return
			}

			var ids = make([]string, 0, len(test.Expect.DocumentIds))
			for rawDocId := range seq {
				ids = append(ids, string(rawDocId))
			}
			if !assertions.Equal(test.Expect.DocumentIds, ids, "expecting other ids") {
				return
			}
		})
	}

}
