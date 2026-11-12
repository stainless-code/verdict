export const records: string[] = [];

export function save(id: string): void {
  records.push(id);
}
