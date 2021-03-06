/*
Copyright 2019 GramLabs, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	crypto_rand "crypto/rand"
	"encoding/binary"
	"math/rand"
	"os"

	"github.com/redskyops/redskyops-controller/redskyctl/internal/commands"
)

func main() {
	// Seed the pseudo random number generator using the cryptographic random number generator
	// https://stackoverflow.com/a/54491783
	var b [8]byte
	_, err := crypto_rand.Read(b[:])
	if err != nil {
		panic(err)
	}
	rand.Seed(int64(binary.LittleEndian.Uint64(b[:])))

	// TODO Determine which command to run by looking at the base name of $0
	command := commands.NewRedskyctlCommand()

	// Run the command
	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
