function sum(values: number[]): number {
  let total = 0;
  let index = 0;
  while (index < values.length) {
    total = total + values[index];
    index = index + 1;
  }
  return total;
}

function first(values: number[]): number {
  return values[0];
}

interface Point {
  x: number;
  y: number;
}

interface Bag {
  items: number[];
}

function makePair(): [number, string] {
  return [5, "five"];
}

function pairFirst(pair: [number, string]): number {
  return pair[0];
}

const nums: number[] = [1, 2, 3];
console.log(sum(nums));
console.log(nums.length);
console.log(nums[1]);
console.log(first(nums));

const grid: number[][] = [[1, 2], [3, 4]];
console.log(grid[1][0]);

const labels: string[] = ["a", "😀"];
console.log(labels[1].length);

const empty: number[] = [];
console.log(empty.length);

const points: Point[] = [{ x: 1, y: 2 }, { x: 3, y: 4 }];
console.log(points[1].y);

const pair: [number, string] = [7, "seven"];
console.log(pair[0]);
console.log(pair[1]);

const chosen = nums.length > 0 ? nums : empty;
console.log(chosen[0]);

const bag: Bag = { items: nums };
console.log(bag.items[2]);

const made = makePair();
console.log(made[0]);
console.log(pairFirst(pair));

type Nums = number[];
type FlagPair = [number, boolean];

const alias: Nums = [9];
console.log(alias[0]);

const flags: FlagPair = [2, false];
console.log(flags[1]);
