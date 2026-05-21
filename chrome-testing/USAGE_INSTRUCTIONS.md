# chromerpc Testing Module — Usage Instructions

This module gives any project the ability to take real browser screenshots of its output
and use those as visual validation or README illustrations. 

The consumer only needs to provide HTML files.

---

## What this module does

It takes HTML files and produces PNG screenshots using a real headless Chrome browser controlled via gRPC.

---

## Prerequisites

- **Go** (to build chromerpc if not already installed)
- **Google Chrome** installed on the system
- **Python 3** (used to serve HTML files over HTTP)

The script handles building and running chromerpc itself.

---

## How to use it in your project

1. Copy this folder from this repo into your project. The script here is self-contained and will build the chromerpc binary automatically if needed.

2. Create simple HTML files that demonstrates the capabilities of your project. The HTML files should render whatever your project produces and that you want to show off. Put them in a folder.

3. Run the script `./chrome-testing/snap.sh` on the html file (or the directory containing the HTML files) and wait for the screenshot PNGs to be captured. Verify that the screenshots are valid.

```bash
# Single HTML file
./chrome-testing/snap.sh path/to/page.html path/to/output.png

# Directory of HTML files
./chrome-testing/snap.sh path/to/html_dir/ path/to/output_dir/
```

4. Reference the screenshot PNG files from your project README file.

---

## Some general tips

Anything your project produces that can be viewed in a browser can be validated visually. The shape of the HTML is entirely up to you and depends on what your project does.

The script does not care what is in the HTML. It just screenshots whatever Chrome renders.

For multi-example projects, the typical flow is:
1. Find the representative examples or fixtures in your codebase
2. For each example, write a simple HTML file that renders it
3. Run the script over the directory of HTML files to get a PNG per example
4. Pick the best ones for the README; keep the rest as regression snapshots

---

## Notes for agents and automated workflows

- The script handles handles Chrome discovery, binary building, port allocation, and cleanup on its own.
- The script exits non-zero on any failure.
- Screenshots open automatically after capture for local/interactive runs.