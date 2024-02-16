# Unit test stub (files)

The files here are used for the unit tests for BRO code that's applicable. 
Each file correlates to a unit test method and contains the mock data that will be used for filtering.
Because each test method is targeting a specific filter scenario, we need the results to be uniform and assertable.
This is the main reason we opt to create a new set of YAML for each test method and scenario.

## Unit Test Function & Stub File Example

There is a 1:1 correspondence between each test function in `collector_test.go` using `abstractFilterByNameTest` and its input YAML in `test/stubs`.
For example,  `TestFilterByName_ExcludeRegexWithWildcardInclude` will get its YAML input from `tests/stubs/TestFilterByName_ExcludeRegexWithWildcardInclude.yaml`.
This YAML data is then unmarshalled into a slice of `unstructured.Unstructured` objects which the filter expects to see.
Each test constructs its own `v1.ResourceSelector` and verifies that the objects filtered by that selector are as expected.