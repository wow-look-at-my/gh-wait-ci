// Strips conflict regions from a file, keeping the "theirs" side.
const fs = require("fs");
const path = process.argv[2];
const lines = fs.readFileSync(path, "utf8").split("\n");
const out = [];
let mode = "none";
for (const line of lines) {
	if (line.startsWith("<<<<<<< ")) {
		mode = "ours";
		continue;
	}
	if (mode === "ours" && line.startsWith("=======")) {
		mode = "theirs";
		continue;
	}
	if (mode === "theirs" && line.startsWith(">>>>>>> ")) {
		mode = "none";
		continue;
	}
	if (mode === "ours") continue;
	out.push(line);
}
fs.writeFileSync(path, out.join("\n"));
