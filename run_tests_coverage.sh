#!/bin/bash

# Set variables
COVERAGE_FILE="coverage.out"
HTML_COVERAGE_FILE="coverage.html"
PACKAGE_PATH="./auditlogs"  # Adjust this to your package path

# Run tests with coverage
go test -v -coverprofile=$COVERAGE_FILE $PACKAGE_PATH

# Check if the coverage file was created
if [ ! -f "$COVERAGE_FILE" ]; then
    echo "Error: Coverage file not created. Tests may have failed."
    exit 1
fi

# Display coverage percentage
go tool cover -func=$COVERAGE_FILE

# Generate HTML coverage report
go tool cover -html=$COVERAGE_FILE -o $HTML_COVERAGE_FILE

# Check if the HTML coverage file was created
if [ ! -f "$HTML_COVERAGE_FILE" ]; then
    echo "Error: HTML coverage file not created."
    exit 1
fi

# Open the HTML coverage report in the default browser
case "$(uname -s)" in
    Linux*)     xdg-open $HTML_COVERAGE_FILE;;
    Darwin*)    open $HTML_COVERAGE_FILE;;
    CYGWIN*|MINGW32*|MSYS*|MINGW*) start $HTML_COVERAGE_FILE;;
    *)          echo "Unsupported operating system. Please open $HTML_COVERAGE_FILE manually.";;
esac

echo "Coverage report generated and opened in browser."
