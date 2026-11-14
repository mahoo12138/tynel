function echo(text: string): string {
  return text;
}

function relabel(): string {
  let label: string = "a";
  label = label + "b";
  return label;
}

const greeting = "hello";
const world = "世界";
const joined = greeting + ", " + world;

console.log(joined);
console.log(joined.length);
console.log(echo(""));
const left = echo("abc");
const right = echo("abd");
console.log(left === left);
console.log(left !== right);
console.log("😀".length);
console.log("é".length);
console.log("e\u0301".length);
console.log(joined === "hello, 世界");

console.log(relabel());

const picked = joined.length > 0 ? echo("yes") : echo("no");
console.log(picked);
