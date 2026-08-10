import type { TFunction } from "i18next";
import {
  DELIVERY_STATUS,
  RECIPIENT_CHANNEL,
  SIGNER_STATUS,
  SIGNING_MODE,
  SIGNING_STATUS,
} from "../api/signing";

// These map the backend's open string values to their i18n copy with literal keys
// (the typed t() rejects a computed key), falling back to the raw value so a new
// backend value renders as itself rather than breaking the screen.

export function modeLabel(t: TFunction, mode: string): string {
  switch (mode) {
    case SIGNING_MODE.parallel:
      return t("signing.modeLabel.parallel");
    case SIGNING_MODE.sequential:
      return t("signing.modeLabel.sequential");
    default:
      return mode;
  }
}

export function signerStatusLabel(t: TFunction, status: string): string {
  switch (status) {
    case SIGNER_STATUS.pending:
      return t("signing.signerStatus.pending");
    case SIGNER_STATUS.signed:
      return t("signing.signerStatus.signed");
    case SIGNER_STATUS.failed:
      return t("signing.signerStatus.failed");
    default:
      return status;
  }
}

export function requestStatusLabel(t: TFunction, status: string): string {
  switch (status) {
    case SIGNING_STATUS.awaitingSignatures:
      return t("signing.requestStatus.awaitingSignatures");
    case SIGNING_STATUS.completed:
      return t("signing.requestStatus.completed");
    case SIGNING_STATUS.failed:
      return t("signing.requestStatus.failed");
    default:
      return status;
  }
}

export function deliveryStatusLabel(t: TFunction, status: string): string {
  switch (status) {
    case DELIVERY_STATUS.notRequested:
      return t("signing.deliveryStatus.notRequested");
    case DELIVERY_STATUS.pending:
      return t("signing.deliveryStatus.pending");
    case DELIVERY_STATUS.delivered:
      return t("signing.deliveryStatus.delivered");
    case DELIVERY_STATUS.failed:
      return t("signing.deliveryStatus.failed");
    default:
      return status;
  }
}

export function channelLabel(t: TFunction, channel: string): string {
  switch (channel) {
    case RECIPIENT_CHANNEL.none:
      return t("signing.channel.none");
    case RECIPIENT_CHANNEL.email:
      return t("signing.channel.email");
    case RECIPIENT_CHANNEL.qerds:
      return t("signing.channel.qerds");
    default:
      return channel;
  }
}
