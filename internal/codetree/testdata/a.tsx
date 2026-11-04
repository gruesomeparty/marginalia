import React from "react";

type Props = { name: string };

export function Hello({ name }: Props) {
  return <div className="hi">{name}</div>;
}

export const Card = (p: Props) => <Hello name={p.name} />;
