package terraform

import "github.com/matt-FFFFFF/tfmodmake/internal/naming"

func toSnakeCase(input string) string {
	return naming.ToSnakeCase(input)
}
