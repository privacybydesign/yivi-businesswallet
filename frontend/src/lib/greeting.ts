type GreetingKey =
  | "dashboard.greeting.morning"
  | "dashboard.greeting.afternoon"
  | "dashboard.greeting.evening";

const NOON = 12;
const EVENING = 18;

export function greetingKey(date = new Date()): GreetingKey {
  const hour = date.getHours();
  if (hour < NOON) return "dashboard.greeting.morning";
  if (hour < EVENING) return "dashboard.greeting.afternoon";
  return "dashboard.greeting.evening";
}
