import { copyFileSync } from "node:fs";

copyFileSync("manifest.json", "dist/manifest.json");
copyFileSync("static/status.html", "dist/status.html");
copyFileSync("static/status.js", "dist/status.js");
