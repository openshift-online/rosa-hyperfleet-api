# Scalable DynamoDB Change Notification — Superseded

> **This document is superseded.** The replica scaling problem it was designed
> to solve has been addressed by the `hyperfleet-dynamo` GSI two-speed polling
> watcher, which replaced DynamoDB Streams in both kube-applier-aws and the
> operator's `statusstream.Manager`.
>
> The GSI approach has no consumer limit, requires no SNS/SQS infrastructure,
> and is operationally simpler. See [dynamodb-strategy.md](dynamodb-strategy.md)
> for the current architecture.
