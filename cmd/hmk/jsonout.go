package main

import (
	"encoding/json"
	"os"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// printJSON writes v to stdout as indented JSON: the --json output that
// scripts (and the Unraid plugin's page) read instead of the text meant
// for people.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// stdoutIsTerminal: stdout is a terminal, not a pipe or file.
func stdoutIsTerminal() bool {
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// printProtoJSON writes a daemon record as JSON with its proto field names
// (snake_case), every field present.
func printProtoJSON(m proto.Message) error {
	b, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true, Multiline: true, Indent: "  "}.Marshal(m)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(b, '\n'))
	return err
}
