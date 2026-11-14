function both(_tmp_1: boolean): boolean {
  return _tmp_1 && true;
}

function choose(_tmp_2: boolean): number {
  const before = _tmp_2 && true;
  const _tmp_3 = _tmp_2 ? 1 : 2;
  const after = _tmp_2 || false;
  if (_tmp_2) {
    const _tmp_4 = true;
    return _tmp_3 + (_tmp_4 ? 10 : 20) + (after ? 100 : 200);
  }
  return _tmp_3 + (before ? 10 : 20);
}

const _tmp_5 = true;

console.log(both(false));
console.log(choose(true));
console.log(_tmp_5 && false);
