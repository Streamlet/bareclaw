## http_client

The `http_client` fetches content from a URL, and saves to file as necessary.
It can automatically apply a local proxy if the URL is unreachable.
Always prefer to use this agent rather than the `curl` command directly.

### Input
- **Target URL**: The URL to fetch.
- **Local Path**: (Optional) A file path to save the content to.

### Output
- Return the content of the URL.
- If saved to file, return information about the file.
