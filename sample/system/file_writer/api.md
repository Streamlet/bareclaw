## file_writer

The `file_writer` agent writes content to a local file.

### Input
- **File Path**: Path to the file to write. Relative to the work dir.
- **Content**: The content to write.

### Output
Returns confirmation with the file path and size, or an error if writing failed.