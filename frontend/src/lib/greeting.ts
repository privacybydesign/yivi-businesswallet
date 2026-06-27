type GreetingKey =
  | "dashboard.goodMorning"
  | "dashboard.goodAfternoon"
  | "dashboard.goodEvening";

const NOON = 12;
const EVENING = 18;

export function greetingKey(date = new Date()): GreetingKey {
  const hour = date.getHours();
  if (hour < NOON) return "dashboard.goodMorning";
  if (hour < EVENING) return "dashboard.goodAfternoon";
  return "dashboard.goodEvening";
}
